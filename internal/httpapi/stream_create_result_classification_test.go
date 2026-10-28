package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/autostream-control-panel/internal/mediaassets"
	"github.com/example/autostream-control-panel/internal/store"
	"github.com/example/autostream-control-panel/internal/streamvisual"
)

func TestP2VisualErrorsSeparateDomainRejectionFromUnknownCommit(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"invalid input", streamvisual.ErrInvalidSettings, 400, "invalid_visual_settings"},
		{"unready variant", mediaassets.ErrIntegrity, 400, "invalid_visual_settings"},
		{"claim rejected", streamvisual.ErrAssetClaim, 409, "asset_claim_failed"},
		{"reference changed", streamvisual.ErrRevisionConflict, 409, "revision_conflict"},
		{"upload limit", mediaassets.ErrCoverUploadLimit, 409, "cover_upload_limit"},
		{"unknown commit", errors.New("private SQL transport details"), 500, "stream_visual_operation_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeStreamVisualError(response, test.err)
			if response.Code != test.status || !strings.Contains(response.Body.String(), `"code":"`+test.code+`"`) || strings.Contains(response.Body.String(), "private SQL") {
				t.Fatalf("incorrect safe classification: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

type p2LostCreateReceipt struct {
	streamvisual.Repository
	creator streamvisual.AtomicCreator
}

func (r p2LostCreateReceipt) CreateStream(ctx context.Context, userID string, input streamvisual.Create) (store.Stream, streamvisual.Settings, error) {
	stream, visual, err := r.creator.CreateStream(ctx, userID, input)
	if err != nil {
		return stream, visual, err
	}
	// The controlled fault represents losing confirmation after commit. Actual
	// stream, settings and assignments were already written by the repository.
	return store.Stream{}, streamvisual.Settings{}, errors.New("private commit receipt unavailable")
}

func TestP2CreateLostCommitReceiptDoesNotBecomeCorrectableValidation(t *testing.T) {
	fixture := newStreamJSONFramingFixture(t, false, "streams.create")
	server := fixture.handler.(*Server)
	server.streamVisual = p2LostCreateReceipt{Repository: server.streamVisual, creator: server.streamVisual.(streamvisual.AtomicCreator)}
	before, err := fixture.streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	response := serveUserJSON(t, fixture.handler, http.MethodPost, "/streams", fixture.payload, fixture.cookie, fixture.csrf)
	after, err := fixture.streams.ListStreams(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusInternalServerError || len(after) != len(before)+1 || strings.Contains(response.Body.String(), "private commit") {
		t.Fatal("lost commit confirmation was treated as a safe input rejection")
	}
}
