package core

import (
	"io"
)

// TemplateExecutor interface for executing templates
type TemplateExecutor interface {
	ExecuteTemplate(wr io.Writer, name string, data any) error
}
