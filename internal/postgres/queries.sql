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

-- name: UpdateTaskStatusCAS :execrows
UPDATE tasks SET status = $1 WHERE id = $2 AND status = $3;

-- name: InsertTaskStatusChangeHistory :exec
INSERT INTO tasks_status_change_history (
    task_id, old_status, new_status
) VALUES (
             $1, $2, $3
         );

-- name: InsertOutbox :one
INSERT INTO outbox (
    message_id, queue_name, payload
) VALUES (
             $1, $2, $3
         )
    RETURNING id;

-- name: GetUnpublishedOutbox :many
SELECT id, message_id, queue_name, payload
FROM outbox
WHERE published_at IS NULL
ORDER BY id
LIMIT $1;

-- name: MarkOutboxPublished :exec
UPDATE outbox SET published_at = NOW() WHERE id = $1;

-- name: InsertInbox :exec
INSERT INTO inbox (
    message_id, task_id
) VALUES (
             $1, $2
         )
ON CONFLICT (message_id) DO NOTHING;

-- name: IsMessageProcessed :one
SELECT EXISTS(SELECT 1 FROM inbox WHERE message_id = $1);
