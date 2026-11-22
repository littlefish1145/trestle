package buildanalyzer

import "context"

type jobsKey struct{}

func WithJobs(ctx context.Context, jobs int) context.Context {
	return context.WithValue(ctx, jobsKey{}, jobs)
}

func Jobs(ctx context.Context) int {
	jobs, _ := ctx.Value(jobsKey{}).(int)
	return jobs
}
