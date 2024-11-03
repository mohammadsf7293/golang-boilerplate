-- this is the migration file for making database up
DROP TABLE tasks_status_change_history;

DROP TABLE tasks;

DROP TYPE task_type;

DROP TYPE task_status;

DROP TYPE task_priority;

DROP FUNCTION update_timestamp;