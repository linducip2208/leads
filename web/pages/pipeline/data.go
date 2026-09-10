package pipeline

import "leadforge/internal/webapp"

// PageMeta is layout metadata passed from the web server.
type PageMeta = webapp.Page

// Option is a select option.
type Option struct {
	Value string
	Label string
}

// DealCard is a kanban card.
type DealCard struct {
	ID      string
	Title   string
	Company string
	Value   string
	Owner   string
}

// StageCol is one kanban column.
type StageCol struct {
	ID    string
	Name  string
	Kind  string
	Deals []DealCard
	Total string
	Count int
}

// BoardData powers the pipeline board.
type BoardData struct {
	PipelineID   string
	PipelineName string
	Pipelines    []Option
	Stages       []StageCol
}
