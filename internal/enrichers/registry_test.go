package enrichers

// Governing: ADR-0015 (type-keyed enricher registry),
// SPEC metadata-enrichment-pipeline REQ-ENRICH-050 (duplicate type registrations MUST return an error)

import (
	"context"
	"testing"

	"spotter/ent"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegistry_Register_Succeeds(t *testing.T) {
	r := NewRegistry()

	err := r.Register(TypeSpotify, func(ctx context.Context, user *ent.User) (Enricher, error) {
		return nil, nil
	})
	require.NoError(t, err)

	_, ok := r.Get(TypeSpotify)
	assert.True(t, ok, "registered factory should be retrievable")
}

// TestRegistry_Register_DuplicateTypeErrors verifies REQ-ENRICH-050: registering
// the same enricher type twice must return an error instead of silently
// overwriting the earlier factory.
func TestRegistry_Register_DuplicateTypeErrors(t *testing.T) {
	r := NewRegistry()

	firstCalled := false
	first := func(ctx context.Context, user *ent.User) (Enricher, error) {
		firstCalled = true
		return nil, nil
	}
	second := func(ctx context.Context, user *ent.User) (Enricher, error) {
		return nil, nil
	}

	require.NoError(t, r.Register(TypeFanart, first))

	err := r.Register(TypeFanart, second)
	require.Error(t, err, "duplicate registration must return an error")
	assert.Contains(t, err.Error(), string(TypeFanart))

	// The original factory must remain registered (no silent overwrite).
	got, ok := r.Get(TypeFanart)
	require.True(t, ok)
	_, _ = got(context.Background(), nil)
	assert.True(t, firstCalled, "original factory must survive a duplicate registration attempt")
}

// TestRegistry_Register_DistinctTypesCoexist verifies duplicate detection does
// not interfere with registering different enricher types.
func TestRegistry_Register_DistinctTypesCoexist(t *testing.T) {
	r := NewRegistry()
	factory := func(ctx context.Context, user *ent.User) (Enricher, error) { return nil, nil }

	for _, typ := range DefaultOrder() {
		require.NoError(t, r.Register(typ, factory), "registering type %q", typ)
	}
	assert.Len(t, r.Types(), len(DefaultOrder()))
}
