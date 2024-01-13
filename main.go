package main

import (
	"context"
	"flag"
)

func main() {
	pat := flag.String("gha-pat", "", "GitHub Actions Personal Access Token")
	owner := flag.String("owner", "", "GitHub Repo Owner")
	repo := flag.String("repo", "", "GitHub Repo Name")
	flag.Parse()

	shutdown, err := setupOTelSDK(context.Background(), "github-actions-otel-exporter", "0.0.1")
	if err != nil {
		panic(err)
	}
	defer shutdown(context.Background())
	listWorkflowJobs(*pat, *owner, *repo)
}
