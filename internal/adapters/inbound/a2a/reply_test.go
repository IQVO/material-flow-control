package a2a

import (
	"context"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// carry (Task 21 review): a message-only agent answers with no Task, so the reply must not carry the taskId the
// SDK mints for every run (a client would GetTask an id that never existed); it keeps the caller's contextId.
func TestTheReplyKeepsTheCallersContextAndNamesNoTask(t *testing.T) {
	client := agent(t, fakeReads{})
	msg := a2a.NewMessage(a2a.MessageRoleUser, skill("station_queue", map[string]any{"stationId": "ST1"}))
	msg.ContextID = "ctx-9"
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok {
		t.Fatalf("reply = %T, want a message", resp)
	}
	if reply.TaskID != "" || reply.ContextID != "ctx-9" || reply.Role != a2a.MessageRoleAgent {
		t.Fatalf("reply taskId %q contextId %q role %v, want no task and the caller's ctx-9", reply.TaskID, reply.ContextID, reply.Role)
	}
}
