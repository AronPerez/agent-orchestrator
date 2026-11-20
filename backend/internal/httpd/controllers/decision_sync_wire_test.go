package controllers_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/decisionsync"
)

func jsonFields(t reflect.Type) map[string]string {
	out := map[string]string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		out[name] = f.Type.Kind().String()
	}
	return out
}

// The HTTP client (decisionsync) and the server (controllers) each declare the
// sync wire messages; their JSON field names and kinds must match exactly.
func TestDecisionSyncWireMatchesDTOs(t *testing.T) {
	for _, pair := range []struct{ dto, wire any }{
		{controllers.DecisionSyncEntry{}, decisionsync.WireEntry{}},
		{controllers.DecisionSyncHead{}, decisionsync.WireHead{}},
		{controllers.DecisionSyncExchangeRequest{}, decisionsync.WireExchangeRequest{}},
		{controllers.DecisionSyncExchangeResponse{}, decisionsync.WireExchangeResponse{}},
		{controllers.DecisionSyncPushRequest{}, decisionsync.WirePushRequest{}},
		{controllers.DecisionSyncPushResponse{}, decisionsync.WirePushResponse{}},
		{controllers.DecisionSyncGap{}, decisionsync.WireGap{}},
		{controllers.DecisionSyncConflict{}, decisionsync.WireConflict{}},
		{controllers.DecisionSyncTokenResponse{}, decisionsync.MintResult{}},
	} {
		dto, wire := jsonFields(reflect.TypeOf(pair.dto)), jsonFields(reflect.TypeOf(pair.wire))
		if !reflect.DeepEqual(dto, wire) {
			t.Errorf("%T fields %v != %T fields %v", pair.dto, dto, pair.wire, wire)
		}
	}
}
