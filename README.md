![taskflow logo](assets/taskflow-logo.webp)

# Taskflow
This project serves as a template for managing and processing tasks using background workers, with tasks stored in a database. While the tasks provided are placeholders within the pkg folder, they demonstrate how you can implement similar functionality in your own projects.
For instance, the project includes sample tasks like send email and run query. These tasks are intentionally simplified—they merely simulate activity by running a sleep function and occasionally failing. The failures are deliberate, allowing you to explore error handling and implement retries when necessary.

This is a sample project designed to be run locally. My goal in creating this repository is to provide a code template that:
Aligns with the [Go project layout standards](https://github.com/golang-standards/project-layout/)
Incorporates best practices and up-to-date libraries
Serves as a solid starting point for those looking to create a new project and in need of a reliable boilerplate.

## Notes
- There is a simple Docker Compose file included that contains a database to work against.
- There is a `.env.template` file that you can use to control env variables for your application. Make a copy of this file with the name `.env` and it will be loaded automatically.
- The existing `Makefile` contains 3 commands: `deps`, `build`, and `test`.
- The application will automatically run database migrations when it starts, make sure to add your migrations into the `./db/migrations` folder. 

# The application APIs
API docs are available in the `api` directory, and you could open them using swagger UI by the following command from the root of the project:
```
docker run -p 8081:8080 -e SWAGGER_JSON=/api/swagger.yaml -v $(pwd)/api:/api swaggerapi/swagger-ui
```
by doing so, you could head to `http://localhost:8081` to see the API docs

Or if you use `IntelliJ Goland`, you could see the file in its Swagger UI tool.

# The application structure
Application consists of 3 parts:
- The API server code
- The worker code for processing jobs (tasks)
- The worker for re-queuing missed jobs

# Environment setup
Before running the application, make sure you have set correct env variables in the `.env` file.
This file contains PostgreSQL credentials, RabbitMQ credentials, Redis Credentials, etc.
After making sure the env file is OK, you have to source it before running the app, this way all env variables will be set.
```
source .env
```
# Application server
This part serves APIs to be accessible using HTTP interface.
You could run this part either by running the following command:
```
    go run cmd/server/main.go
```
Or by building application and then running the server binary as follows:
```
    make build
    ./bin/server
```

# Worker(s)
There is job workers in the `cmd/worker` directory.
The reason I've separated them is:
- In this way, I think we could have a better setup for liveness probes, and health checkers.
Because when you fire some go-routines, they are not as easily observable as a single process.
- By Making the workers separated, we could delegate the task of scaling the workers to Kubernetes.

I have used `RabbitMQ` here; When a task is created through API, it's serialized and pushed to a jobs queue, then the worker(s) are able to pop the task from the queue and process that.
I have used 4 queues:
- A queue for tasks with `high` priority
- A queue for tasks with `normal` priority
- A queue for tasks with `low` priority
- A queue for `integration testing` which won't be used on production

Why RabbitMQ?

I have worked with `Redis` queues in high loads, and it's not efficient in high loads, Also I thought that using `Kafka` would be considered as over-engineering for this case. So, I preferred to use `Rabbit Pub/Sub`.

I also have used `Redis` as `distributed lock` infrastructure in the workers. When the worker starts to process a task, It locks a key to make sure that other workers can't process and are not processing that task simultaneously.
Although the code doesn't push a task multiple times to the queue, I also considered this case.

Each Worker has a number that must be unique. numbers could start from 1 to the infinite.
When you want to run a worker, you could run the following command:
```
go run cmd/worker/main.go $priority $workerNumber
```
Priority could be of `high`, `normal` or `low`.

Example:
```
go run cmd/worker/main.go normal 1
```

Or you could build the app (if you haven't built that before), and use the binary as follows:
```
./bin/job_worker $priority $workerNumber
```
This way and by using a queue, we could easily scale the number of workers if load is high without increasing pressure on the `PostgreSQL`.

# Recovery Worker
I've considered `durability` of RabbitMQ to be true.
But, In case that data of the queue has been lost or some tasks have got out of the queue, you could run this command as follows:
```
go run cmd/recovery/main.go $taskStatus $pastSeconds $limit
```
or
```
./bin/queue_recovery $taskStatus $pastSeconds $limit
```
Parameters definitions:
- taskStatus: It must only be `queued` or `failed` because `running` or `succeeded` jobs are not supposed to be re-queued.
- pastSeconds: It defines the number of seconds which is past and the `updated_at` field of the task is not changed (It fetches tasks with the given status which are not updated in the last X seconds)
- limit: It defines the maximum number of items to be fetched (a controller parameter for the cases which there are lots of tasks to be re-queued)

Normally this command is not needed to be run, it's just been developed for emergency cases.

# Building the app
As mentioned before, the needed scripts for building the app are developed in the make file, so all you need is to run:
```bash
make build
```
Whenever you change the code, and then, binaries will be available in the `bin` directory of the project.

# Deploying app to the Kubernetes
There is a Dockerfile developed for building the docker file of the app.
You can make a new docker image of the application using the following command:
```bash
    make docker
```
In this Dockerfile, all binaries (server, job_worker, queue_recovery) are placed into one image.
In the future, If the size of binaries goes high, we could have different Dockerfiles for different usages(one for appserver, one for workers, one for recovery).
But, here for the sake of simplicity, I've put all binaries in one Dockerfile.

After you made the docker image, the `make docker` command will let you know the name of created image.
Then, you have to:
- Load that Docker image to kind using the following command:
```
kind load docker-image $dockerImageName
```
- Update the image name in `k8s` files: there are Kubernetes deployment files placed in the `k8s/deployment` directory.
You have to update the docker image (image) field of these files to the newly created image.

# Cluster Creation
You have to setup a local cluster using kind if you haven't done that.
You should install `kind` and then run the following command:
```
kind create cluster
```

# Installation of required infras
You should deploy `PostgreSQL`, `Redis`, and `RabbitMQ`.
To have them deployed on the same Kubernetes cluster, you could use `helm`.
Make sure you have installed it and then run the following commands.
At the end of each command, it will show you Readmes to know how to find out their credentials.
So, please write them down for each step(infra), As we need them in the next step.

Commands to deploy these infras are:
```
helm install my-postgresql oci://registry-1.docker.io/bitnamicharts/postgresql
helm install my-rabbit oci://registry-1.docker.io/bitnamicharts/rabbitmq
helm install my-redis oci://registry-1.docker.io/bitnamicharts/redis
```

# Preparation of configs
Project configs are placed in the `k8s/helm_charts/myvalues.yaml` file down to `env:` section.
Make sure you have set current configs here.
Note!: Here I've placed important data like Database credentials here, but in production environments, you'd better store them as secrets for more security.

# Deploying deployments
I have developed helm charts for deployment of server and workers.
I have used Helm charts because there are lots of repetitious things in this kube files and using Helm is better.
Before helm, I had used K8s files which are available in my previous commits (you could look at them).
I replaced them with helm charts.

To deploy the Appserver, you have to run:
```
helm upgrade --install appserver ./k8s/helm_charts -f ./k8s/helm_charts/myvalues.yaml
```

To deploy workers you have to run these commands:
```
helm upgrade --install jobworker-high-1  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_high_1.yaml
helm upgrade --install jobworker-normal-1  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_normal_1.yaml
helm upgrade --install jobworker-low-1  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_low_1.yaml
```

In the next step, Make sure the pods of both commands are working, by listing their pods and then checking their state (their state must be `Running`).
You should also check their logs by using the following command:
```
kubectl logs -f $podName
```
For finding the pod names you could run the following command:
```
kubectl get pods
```
The pod names are the same as `deployment` names plus some added hash suffixes.

If there are problems you should see the logs and be able to resolve it.
If everything works, go to the next stage

# Exposing the appserver
To expose the service in production environment, you should connect the service of appserver to the ingress to connect it to the external edge of the infra.
But here on your local cluster, you don't need to do so. You could just expose the created service for the `server` app to be connected to specific port of your localhost.
To do so, you could run the following command:
```
kubectl port-forward svc/appserver-myapp 8080:8086
```
This command will map the port 8080 of your local machine to the port 80 of the server service. 
Now you can access your app server by sending your requests to `http://localhost:8080`.

# How to update deployments
If you made changes to the code, then in order to deploy your changes to Kubernetes, you need to:
1 - Build again your application (`make build`)
2 - Create new Docker image (`make docker`)
3 - Load new image in Kind (`kind load docker-image $newDockerImage`)
4 - Update image version in the `k8s/helm_charts/myvalues.yaml` file `version` field.
For example, if your image name is `task-manager:v23123-8`, you should set the `version` field to `v23123-8`.
4 - Update helm charts.

For Appserver, you have to run the following command:
```
helm upgrade --install appserver ./k8s/helm_charts -f ./k8s/helm_charts/myvalues.yaml
```

For workers you should do as follows:
```
helm upgrade --install jobworker-high-1  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_high_1.yaml
helm upgrade --install jobworker-normal-1  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_normal_1.yaml
helm upgrade --install jobworker-low-1  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_low_1.yaml
```

After this step, just recheck that you may need to rerun `kubectl port-forward ...` command.
All done!

# Automation of building and deploying application to local
I've provided you with the `build_and_deploy.sh` script. all you need is to run that. But make sure you have created the infra by doing the previous steps before running this script.
This script is a sample of what CI/CD does, but it's written in bash to be able to be run locally.

Note: If you've created new deployments (new helm files), make sure to update this script and add those stages.

Note: In production, for providing automation for building and deploying, we should define a pipeline file (CI/CD file). The logic of that file will be very near to this script, except: 
- Instead of doing `kind load docker-image $dockerImage`, we should have a Docker Repository and push the docker images into that
- Instead of doing `kubectl port-forward svc/appserver-myapp 8080:8086`, we should have an Ingress service connected to appserver service => So, this stage will be omitted

After running the `build_and_deploy` command, just recheck that you may need to rerun `kubectl port-forward ...` command.

# Scaling
In the future, if you need more than one worker, you should create more workers (by different worker numbers) by creating new values files by copying and changing current worker values file and edit their corresponding params.
For example:
- cp `k8s/helm_charts/myvalues_job_worker_high_1.yaml` to `k8s/helm_charts/myvalues_job_worker_high_2.yaml` and edit the worker number arg to 2.
- Install the new created helm chart:
```
helm upgrade --install jobworker-high-2  ./k8s/helm_charts/ -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_high_2.yaml
```

# Testing
At first make sure you have installed `godotenv` command. If you haven't installed that, please install that using the following command:
```
go install github.com/lpernett/godotenv/cmd/godotenv@latest
```
Then you could run `make test` to run all the tests.
There are some unit tests developed in the `pkg` folder for `pkg/email` and `pkg/run_query` methods.
For the rest of system, I suggest to write some unit tests for `server APIs` and `job worker` logic.

# Integration test
There are some integration tests developed in `cmd/server/main_test.go` file.
You could run exactly these tests using:
```
godotenv -f .env go test -v cmd/server/*
```
I also have changed Makefile to use godotenv before running all the test, so Make sure you've installed `godotenv` before running tests.

All other scenarios which needed to be integration tested, are written in the file `cmd/server/main_test.go` as `TODO`s.

# Timeout configs
There are two timeout configs added in the .env file (and also in configmap file which needs to be checked).
`SERVER_TIME_OUT_IN_SECONDS` and `WORKER_TIME_OUT_IN_SECONDS`
I have set their default values as follows:
```
SERVER_TIME_OUT_IN_SECONDS = 5
```
Which means that an HTTP request will be cancelled if it lasts longer than 5 seconds.
And for Worker timeout, I have set default as follows:
```
WORKER_TIME_OUT_IN_SECONDS = 15
```
I have considered 15 seconds because the run query tasks takes 3 seconds to run, and if it fails, I'll try to redo that for upto 5 times which the whole operation might take upto 15 seconds.

# Retrials
For failure of doing tasks, or connection retrial of infras (Postgres, Redis, Rabbit), I have used `"github.com/cenkalti/backoff/v4"` library which is so straightforward to use.

# Health checkers
I've also added separated Health checker APIs (`liveness` and `readiness`) to the worker code.
So It will also expose the server port for serving `liveness` and `readiness` APIs.
This feature only works for Kubernetes liveness and readiness probes.
If you run the worker on the same machine as you have run the server, its health API server couldn't run.

# PostgreSQL sqlc library
I have used the `https://github.com/sqlc-dev/sqlc` library for development of database layer.
Configs of that are stored in `sqlc.yaml` file in the root of project.
Schemas, and migrations are defined at the same files in `db/migrations` directory. 
If you want to make change in things, You should:
- Write migrations if needed in `db/migrations` path
- Edit or write new queries in `internal/postgres/queries.sql` file
- Run the `sqlc generate` command in the root of the project (You must have installed `sqlc` command before this step)
- Edit the `internal/postgres/storage.go` file and add the new methods or edit related methods
- Don't forget to update `internal/domain/storage.go` interface if you changed method signatures or added new method to `internal/postgres/storage.go` file
All Done


# Things to improve for the future
There are some points to improve in this project:
- If you find that incorporating Distributed Lock, RabbitMQ, and Redis adds unnecessary complexity to your project, you might consider using the `Postgres skip locked` feature as a simpler alternative. In that way you will be using Postgres as a queue by defining a table for queueing tasks in that and will get rid of using Redis and RabbitMQ. However, if you choose this approach, be sure to remove the update_timestamp trigger. Keeping this trigger active can lead to performance issues when locking table records.
- For implementing commands, I recommend using Go command-line utilities like [Cobra](https://github.com/spf13/cobra). Cobra provides a robust framework for creating powerful and flexible CLI applications in Go. Here I have used simple Go main functions but Cobra is much better for prod envs.
- In the production envs, please separate secret configs (in `helm envs` or in `k8s configmaps`) from non-secrets.
- In the production envs, in the Dockerfiles, you should pin the base image to a specific version rather using `latest` images.
- Regarding the use of Redis lock keys, I've based the implementation on the assumption that tasks will take no more than 3 seconds to complete. Therefore, I've set the expiration time for Redis lock keys to 10 seconds. In practice, it's crucial to understand the execution time of your tasks and adjust the lock key timeout accordingly if you are utilizing Redis's distributed lock feature. This ensures that your lock durations are appropriately tailored to your specific task requirements.
- For the sake of simplicity, you could use `Golang standard HTTP` instead of using `Gin`.