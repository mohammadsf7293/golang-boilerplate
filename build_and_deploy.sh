#!/bin/bash

NO_COLOR="\033[0m"
OK_COLOR="\033[32;01m"
ERROR_COLOR="\033[31;01m"

echo -e "${OK_COLOR}==> Running tests to make sure the application is OK ...${NO_COLOR}"
if ! make test; then
    echo -e "${ERROR_COLOR}==> Tests failed. Exiting...${NO_COLOR}"
    exit 1
fi

echo -e "${OK_COLOR}==> Building the application to make sure it's buildable and there is no error in code ...${NO_COLOR}"
if ! make build; then
    echo -e "${ERROR_COLOR}==> Build failed. Exiting...${NO_COLOR}"
    exit 1
fi

echo -e "${OK_COLOR}==> Making docker image for the app ...${NO_COLOR}"
DATE=$(date -u +%Y.%m.%d-%H%M%S)
COMMIT_SHORT_HASH=$(git rev-parse --short HEAD)
VERSION=v$DATE-$COMMIT_SHORT_HASH
DOCKER_IMAGE_NAME=task-manager:$VERSION
if ! docker build -t $DOCKER_IMAGE_NAME .; then
    echo -e "${ERROR_COLOR}==> Failed to build Docker image ...${NO_COLOR}"
    exit 1
fi
echo -e "${OK_COLOR}==> Successfully built docker image $DOCKER_IMAGE_NAME ...${NO_COLOR}"

if ! kind load docker-image $DOCKER_IMAGE_NAME; then
    echo -e "${ERROR_COLOR}==> Failed to load docker image in kind docker image registry ...${NO_COLOR}"
    exit 1
fi
echo -e "${OK_COLOR}==> Successfully loaded docker image $DOCKER_IMAGE_NAME into kind docker image registry ...${NO_COLOR}"

if ! helm upgrade --install appserver ./k8s/helm_charts --set app.version=$VERSION -f ./k8s/helm_charts/myvalues.yaml; then
    echo -e "${ERROR_COLOR}==> Failed to load update container image of server helm chart ...${NO_COLOR}"
    exit 1
fi
echo -e "${OK_COLOR}==> Successfully updated version of server helm chart to $VERSION ...${NO_COLOR}"

if ! helm upgrade --install jobworker-high-1 ./k8s/helm_charts --set app.version=$VERSION -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_high_1.yaml; then
    echo -e "${ERROR_COLOR}==> Failed to load update container image of jobworker-high-1 helm chart ...${NO_COLOR}"
    exit 1
fi
echo -e "${OK_COLOR}==> Successfully updated version of jobworker-high-1 helm chart to $VERSION ...${NO_COLOR}"

if ! helm upgrade --install jobworker-normal-1 ./k8s/helm_charts --set app.version=$VERSION -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_normal_1.yaml; then
    echo -e "${ERROR_COLOR}==> Failed to load update container image of jobworker-normal-1 helm chart ...${NO_COLOR}"
    exit 1
fi
echo -e "${OK_COLOR}==> Successfully updated version of jobworker-normal-1 helm chart to $VERSION ...${NO_COLOR}"

if ! helm upgrade --install jobworker-low-1 ./k8s/helm_charts --set app.version=$VERSION -f ./k8s/helm_charts/myvalues.yaml -f ./k8s/helm_charts/myvalues_job_worker_low_1.yaml; then
    echo -e "${ERROR_COLOR}==> Failed to load update container image of jobworker-low-1 helm chart ...${NO_COLOR}"
    exit 1
fi
echo -e "${OK_COLOR}==> Successfully updated version of jobworker-low-1 helm chart to $VERSION ...${NO_COLOR}"