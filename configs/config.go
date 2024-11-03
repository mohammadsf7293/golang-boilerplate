package configs

import (
	"fmt"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
	"log"
	"os"
)

type Config struct {
	ServerPort             string `envconfig:"SERVER_PORT" default:"8080"`
	ServerTimeOutInSeconds int64  `envconfig:"SERVER_TIME_OUT_IN_SECONDS" default:5`
	WorkerTimeOutInSeconds int64  `envconfig:"WORKER_TIME_OUT_IN_SECONDS" default:15`
	Database               DatabaseConfig
	RabbitMQ               RabbitMQConfig
	RedisConfig            RedisConfig
}

type DatabaseConfig struct {
	Username     string `envconfig:"DB_USERNAME"`
	Password     string `envconfig:"DB_PASSWORD"`
	Host         string `envconfig:"DB_HOST"`
	Port         string `envconfig:"DB_PORT"`
	Database     string `envconfig:"DB_DATABASE"`
	DatabaseTest string `envconfig:"DB_DATABASE_TEST"`
	SSLMode      string `envconfig:"DB_SSL_MODE" default:"require"`
	PoolMaxConns int    `envconfig:"DB_POOL_MAX_CONNS" default:"1"`
}

type RabbitMQConfig struct {
	Username                    string `envconfig:"RABBIT_USERNAME"`
	Password                    string `envconfig:"RABBIT_PASSWORD"`
	Host                        string `envconfig:"RABBIT_HOST"`
	Port                        string `envconfig:"RABBIT_PORT"`
	NormalPriorityJobsQueueName string `envconfig:"NORMAL_PRIORITY_JOBS_QUEUE_NAME"`
	HighPriorityJobsQueueName   string `envconfig:"HIGH_PRIORITY_JOBS_QUEUE_NAME"`
	LowPriorityJobsQueueName    string `envconfig:"LOW_PRIORITY_JOBS_QUEUE_NAME"`
	TestJobsQueueName           string `envconfig:"TEST_JOBS_QUEUE_NAME"`
}

type RedisConfig struct {
	Username string `envconfig:"REDIS_USERNAME"`
	Password string `envconfig:"REDIS_PASSWORD"`
	Host     string `envconfig:"REDIS_HOST"`
	Port     string `envconfig:"REDIS_PORT"`
	DBIndex  int32  `envconfig:"REDIS_DB_INDEX"`
}

// ToMigrationUri returns a string specifically for the migration package with the right prefix
func (d DatabaseConfig) ToMigrationUri() string {
	return fmt.Sprintf("pgx5://%s:%s@%s:%s/%s?sslmode=%s",
		d.Username,
		d.Password,
		d.Host,
		d.Port,
		d.Database,
		d.SSLMode,
	)
}

// ToTestMigrationUri returns a string specifically for the migration package with the right prefix for test database
func (d DatabaseConfig) ToTestMigrationUri() string {
	return fmt.Sprintf("pgx5://%s:%s@%s:%s/%s?sslmode=%s",
		d.Username,
		d.Password,
		d.Host,
		d.Port,
		d.DatabaseTest,
		d.SSLMode,
	)
}

// ToDbConnectionUri returns a connection URI to be used with the pgx package
func (d DatabaseConfig) ToDbConnectionUri() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&pool_max_conns=%d",
		d.Username,
		d.Password,
		d.Host,
		d.Port,
		d.Database,
		d.SSLMode,
		d.PoolMaxConns,
	)
}

// ToTestDBConnectionUri returns a string specifically for running the integration tests
func (d DatabaseConfig) ToTestDBConnectionUri() string {
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s&pool_max_conns=%d",
		d.Username,
		d.Password,
		d.Host,
		d.Port,
		d.DatabaseTest,
		d.SSLMode,
		d.PoolMaxConns,
	)
}

// ToRabbitConnectionUri returns a connection URI to be used with the rabbitmq/amqp091-go package
func (d RabbitMQConfig) ToRabbitConnectionUri() string {
	return fmt.Sprintf("amqp://%s:%s@%s:%s/",
		d.Username,
		d.Password,
		d.Host,
		d.Port,
	)
}

// GetMainQueueNames returns a list of important queue names which must be defined before running workers
func (d RabbitMQConfig) GetMainQueueNames() []string {
	return []string{d.HighPriorityJobsQueueName, d.NormalPriorityJobsQueueName, d.LowPriorityJobsQueueName}
}

// GetMainQueueNamesForTest returns a list of important queue names which must be defined before running workers
// In the test mode, I have listed all main queues to be one separated queue for testing, however, it could be changed to have test queues for each priority in the future
func (d RabbitMQConfig) GetMainQueueNamesForTest() []string {
	return []string{d.TestJobsQueueName, d.TestJobsQueueName, d.TestJobsQueueName}
}

// ToRedisConnectionUri returns a connection URI to be used with the redis/go-redis/v9 package
func (d RedisConfig) ToRedisConnectionUri() string {
	return fmt.Sprintf("redis://%s:%s@%s:%s/%d",
		d.Username,
		d.Password,
		d.Host,
		d.Port,
		d.DBIndex,
	)
}

func InitConfig() *Config {
	err := godotenv.Load()

	if err != nil && !os.IsNotExist(err) {
		log.Fatalf("Unable to load .env %v", err)
	}

	var cfg Config
	err = envconfig.Process("", &cfg)
	if err != nil {
		fmt.Print("Cannot load env")
	}

	return &cfg
}
