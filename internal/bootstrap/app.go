package bootstrap

import (
	"net/http"

	"example.com/solo-0021-metrology-assurance-suite/internal/assurance"
	"example.com/solo-0021-metrology-assurance-suite/internal/httpapi"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

func NewHandler() http.Handler {
	repo := repository.New()
	service := assurance.NewService(repo)
	return httpapi.NewHandler(service)
}
