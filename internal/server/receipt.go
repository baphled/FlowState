package server

import (
	"encoding/json"
	"net/http"
)

// receiptVerdictWire translates the verdict DTO into its wire object
// (the item shape of schemas/cli.json key receipt-validate, shared
// with the CLI receipt validate output lines).
func receiptVerdictWire(v ReceiptVerdict) receiptVerdictBody {
	return receiptVerdictBody(v)
}

// receiptValidateHandler answers POST /receipt/validate
// {"path": "..."} with the receipt verdict in the receipt-validate
// item shape. Mirroring the CLI's per-file verdict line, an invalid
// receipt is a 200 "invalid" verdict rather than an error; the
// port's error return is reserved for failures that carry a contract
// code and renders through the shared table (receipt_invalid → 422).
// A malformed body or a missing path is 400 usage.
func (s *Server) receiptValidateHandler(w http.ResponseWriter, r *http.Request) {
	var req pathRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "invalid receipt body")
		return
	}
	if req.Path == "" {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "path is required")
		return
	}
	v, err := s.ports.ValidateReceipt(r.Context(), req.Path)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, receiptVerdictWire(v))
}
