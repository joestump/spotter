// Governing: ADR-0017 (vibes generator abstraction), ADR-0007 (event bus), issue #339

package handlers

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"
)

// mixtapeRecoveryTimeout bounds the database write the panic path makes, so a
// wedged connection cannot leak the goroutine that is reporting the failure.
const mixtapeRecoveryTimeout = 10 * time.Second

// recoverMixtapeGeneration turns a panic raised inside an async mixtape
// generation goroutine into the same user-visible failure a returned error
// produces: the server stays up, the mixtape records why it failed, and the
// SSE error event still reaches the UI. Call it as the first statement of every
// goroutine that generates a mixtape.
func (h *Handler) recoverMixtapeGeneration(userID int, mixtapeID int, mixtapeName string) {
	recovered := recover()
	if recovered == nil {
		return
	}

	err := fmt.Errorf("unexpected error during mixtape generation: %v", recovered)
	h.Logger.Error("recovered panic in mixtape generation",
		"user_id", userID,
		"mixtape_id", mixtapeID,
		"error", err,
		"stack", string(debug.Stack()))

	ctx, cancel := context.WithTimeout(context.Background(), mixtapeRecoveryTimeout)
	defer cancel()

	if _, saveErr := h.Client.Mixtape.UpdateOneID(mixtapeID).
		SetGenerationError(err.Error()).
		Save(ctx); saveErr != nil {
		h.Logger.Error("failed to save mixtape error", "error", saveErr)
	}

	if h.Bus != nil {
		h.Bus.PublishMixtapeError(userID, mixtapeID, mixtapeName, err.Error())
	}
}
