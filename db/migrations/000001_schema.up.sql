-- this is the migration file for making database up

CREATE TYPE task_type AS ENUM ('send_email', 'run_query');

CREATE TYPE task_status AS ENUM ('queued', 'running', 'failed', 'succeeded');

CREATE TYPE task_priority AS ENUM ('high', 'normal', 'low');

CREATE TABLE tasks(
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    type task_type NOT NULL,
    status task_status DEFAULT 'queued' NOT NULL,
    priority task_priority DEFAULT 'normal' NOT NULL,
    payload JSON NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Create a trigger function to update the updated_at field
CREATE OR REPLACE FUNCTION update_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Create the trigger to call the function before each row update
CREATE TRIGGER update_tasks_table_updated_at
    BEFORE UPDATE ON tasks
    FOR EACH ROW
    EXECUTE FUNCTION update_timestamp();

CREATE TABLE tasks_status_change_history(
      id SERIAL PRIMARY KEY,
      task_id INTEGER REFERENCES tasks(id) NOT NULL,
      old_status task_status NOT NULL,
      new_status task_status NOT NULL,
      created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);