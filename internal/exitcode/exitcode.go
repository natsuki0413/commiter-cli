package exitcode

const (
	Success      = 0
	Internal     = 1
	Usage        = 2
	Canceled     = 3
	Safety       = 4
	LLM          = 5
	Verification = 6
	Commit       = 7
	Push         = 8
	Interrupted  = 130
)

// Error carries the public exit classification without exposing an underlying
// error that may contain an untrusted or sensitive value.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Message }

func New(code int, message string) error {
	return &Error{Code: code, Message: message}
}

func Code(err error) int {
	if err == nil {
		return Success
	}
	if classified, ok := err.(*Error); ok {
		return classified.Code
	}
	return Internal
}
