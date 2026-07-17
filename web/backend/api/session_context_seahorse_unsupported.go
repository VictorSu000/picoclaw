//go:build mipsle || netbsd || (freebsd && arm)

package api

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func (h *Handler) reconcileEditedSessionContext(
	_ context.Context,
	_ string,
	_ string,
	_ []providers.Message,
) error {
	return nil
}
