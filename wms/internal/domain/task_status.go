package domain

type TaskStatus string

const (
	TaskStatusPending    TaskStatus = "PENDING"
	TaskStatusInProgress TaskStatus = "IN_PROGRESS"
	TaskStatusDone       TaskStatus = "DONE"
	//nolint:misspell // keep exact PostgreSQL enum value from migration: CANCELLED
	TaskStatusCancelled TaskStatus = "CANCELLED"
)
