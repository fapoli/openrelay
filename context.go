package main

import "context"

type contextKey int

const projectKey contextKey = iota

func withProject(ctx context.Context, p *Project) context.Context {
	return context.WithValue(ctx, projectKey, p)
}

func projectFromCtx(ctx context.Context) *Project {
	return ctx.Value(projectKey).(*Project)
}
