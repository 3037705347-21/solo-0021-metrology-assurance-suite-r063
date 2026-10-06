package httpapi

import (
	"net/http"

	"example.com/solo-0021-metrology-assurance-suite/internal/assurance"
)

type Server struct {
	service *assurance.Service
	mux     *http.ServeMux
}

func NewHandler(service *assurance.Service) http.Handler {
	server := &Server{
		service: service,
		mux:     http.NewServeMux(),
	}
	server.routes()
	return server
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("POST /devices", s.registerDevice)
	s.mux.HandleFunc("GET /devices/{id}", s.device)
	s.mux.HandleFunc("POST /cases", s.openCase)
	s.mux.HandleFunc("GET /cases/{id}", s.caseView)
	s.mux.HandleFunc("POST /cases/{id}/measurements", s.recordMeasurement)
	s.mux.HandleFunc("POST /cases/{id}/reopen", s.reopenCase)
	s.mux.HandleFunc("POST /cases/{id}/seal", s.sealCase)
	s.mux.HandleFunc("GET /cases/{id}/events", s.events)
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	s.mux.ServeHTTP(writer, request)
}
