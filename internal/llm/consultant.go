package llm

import "context"

type Opinion struct {
	Consultant string
	Answer     string
	Confidence float64
}

type ConsultantClient interface {
	Consult(ctx context.Context, context, question string) (*Opinion, error)
}
