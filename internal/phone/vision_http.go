package phone

import (
	"encoding/json"
	"net/http"
	"time"
)

func (s *Service) StreamFrames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET for vision streams.", http.StatusMethodNotAllowed)
		return
	}
	sub, err := s.SubscribeFrames(r.Context(), r.URL.Query().Get("device"))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(Result(nil, err))
		return
	}
	defer sub.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	control := http.NewResponseController(w)
	encoder := json.NewEncoder(w)
	for {
		snapshot, err := sub.Next(r.Context())
		if err != nil {
			return
		}
		if err = control.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return
		}
		if err = encoder.Encode(snapshot); err != nil {
			return
		}
		if err = control.Flush(); err != nil {
			return
		}
		if snapshot.State == "offline" {
			return
		}
	}
}
