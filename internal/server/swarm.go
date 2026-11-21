package server

import (
	"net/http"
)

// swarmValidationWire translates the validation DTO into its wire
// object (schemas/cli.json key swarm-validate, shared with the CLI
// swarm validate output).
func swarmValidationWire(v SwarmValidation) swarmValidationBody {
	return swarmValidationBody(v)
}

// swarmRunWire translates the run DTO into its wire object
// (schemas/cli.json key swarm-run); empty lists render as [] so the
// body keeps the schema's array shapes.
func swarmRunWire(r SwarmRun) swarmRunBody {
	admitted := r.Admitted
	if admitted == nil {
		admitted = []string{}
	}
	children := make([]swarmChildBody, 0, len(r.Children))
	for _, c := range r.Children {
		children = append(children, swarmChildBody(c))
	}
	return swarmRunBody{Manifest: r.Manifest, Admitted: admitted,
		Accepted: r.Accepted, Children: children}
}

// swarmValidateHandler answers POST /swarm/validate {"path": "..."}
// with the manifest verdict in the swarm-validate wire shape. The
// localhost tool server mirrors the CLI contract exactly: the
// manifest is a path on the operator's machine. A malformed body or a
// missing path is 400 usage; port failures flow through the shared
// code table (the cmd closure wraps manifest failures into usage).
func (s *Server) swarmValidateHandler(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if !s.decodeBody(w, r, &req, "invalid swarm validate body") {
		return
	}
	if req.Path == "" {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "path is required")
		return
	}
	v, err := s.ports.ValidateSwarm(r.Context(), req.Path)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, swarmValidationWire(v))
}

// swarmRunHandler answers POST /swarm/run
// {"parent": "...", "path": "...", "budget": N} with the run result
// in the swarm-run wire shape. Body validation mirrors the CLI's
// --parent/--budget gate: a missing parent, path or budget — or a
// non-positive budget — is 400 usage. Port failures flow through the
// shared code table: an unknown parent is 404 session_not_found, a
// terminal parent 409 session_terminal, budget stops 409
// budget_exceeded, and a not-accepted gate outcome surfaces the app's
// unwrapped not-accepted error (ErrorCode → internal → 500), the
// same outcome the CLI exit path takes.
func (s *Server) swarmRunHandler(w http.ResponseWriter, r *http.Request) {
	var req swarmRunRequest
	if !s.decodeBody(w, r, &req, "invalid swarm run body") {
		return
	}
	if req.Parent == "" {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "parent is required")
		return
	}
	if req.Path == "" {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "path is required")
		return
	}
	if req.Budget == nil {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "budget is required")
		return
	}
	if *req.Budget <= 0 {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "budget must be positive")
		return
	}
	res, err := s.ports.RunSwarm(writeContext(r), req.Parent, req.Path, *req.Budget)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, swarmRunWire(res))
}
