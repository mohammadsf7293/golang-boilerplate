-- name: GetTaskByID :one
SELECT * FROM tasks WHERE id = $1;

-- name: GetTasksByStatus :many
SELECT * FROM tasks WHERE status = $1;

-- name: GetLimitedTasksByStatus :many
SELECT * FROM tasks WHERE status = $1 LIMIT $2;

-- name: GetTaskStatusChangeHistory :many
SELECT * FROM tasks_status_change_history WHERE task_id = $1;

-- name: GetMissedTasks :many
SELECT *
FROM tasks
WHERE status = $1 AND updated_at <= now() - ($2 * interval '1 second') LIMIT $3;

-- name: InsertTask :one
INSERT INTO tasks (
    name, type, status, priority, payload
) VALUES (
             $1, $2, $3, $4, $5
         )
    RETURNING id;

-- name: UpdateTaskStatus :exec
UPDATE tasks SET status = $1 WHERE id = $2;

-- name: InsertTaskStatusChangeHistory :exec
INSERT INTO tasks_status_change_history (
    task_id, old_status, new_status
) VALUES (
             $1, $2, $3
         );