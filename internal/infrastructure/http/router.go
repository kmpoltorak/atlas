package httpinfrastructure

import "net/http"

type Router struct {
	Handler http.Handler
}

func NewRouter(handler http.Handler) *Router {
	return &Router{Handler: handler}
}
