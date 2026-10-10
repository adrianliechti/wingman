package docling

import "encoding/json"

type TaskStatus string

const (
	TaskStatusPending TaskStatus = "pending"
	TaskStatusStarted TaskStatus = "started"
	TaskStatusSuccess TaskStatus = "success"
)

type TaskResult struct {
	TaskID     string     `json:"task_id"`
	TaskStatus TaskStatus `json:"task_status"`
	Status     string     `json:"status"`

	Document *Document `json:"document"`
}

type Document struct {
	Filename string `json:"filename"`

	Text     string `json:"text_content"`
	Html     string `json:"html_content"`
	Markdown string `json:"md_content"`

	Json json.RawMessage `json:"json_content"`
}
