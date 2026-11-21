package memory_test

import (
	"context"
	"errors"
	"testing"

	"github.com/IQVO/material-flow-control/internal/adapters/outbound/memory"
	"github.com/IQVO/material-flow-control/internal/application/subsystem"
)

// carry (Task 8 review): RecommendedInputPort is a read; it records the call but neither consumes the next queued
// Answer nor counts as a created job, so the queued refusal reaches CreateStorageJob and the first job is sj-1.
func TestARecommendedInputPortReadConsumesNoAnswerAndCreatesNoJob(t *testing.T) {
	ctx := context.Background()
	a := memory.NewAsrs()
	a.Recommended = "IN2"
	full := &subsystem.Refusal{Status: 422, Code: "storage-full", Retryable: true}
	a.Answer(full)
	if id, err := a.RecommendedInputPort(ctx, "ASRS1"); id != "IN2" || err != nil {
		t.Fatalf("RecommendedInputPort = %q %v", id, err)
	}
	req := subsystem.StorageJobRequest{Kind: "STORE", ContainerID: "TOTE-1", PortID: "IN2", Reference: "to-1.2.1"}
	if _, err := a.CreateStorageJob(ctx, "to-1.2.1", "ASRS1", req); !errors.Is(err, full) {
		t.Fatalf("the queued refusal was not returned to CreateStorageJob: %v", err)
	}
	acc, err := a.CreateStorageJob(ctx, "to-1.2.1", "ASRS1", req)
	if err != nil || acc.ID != "sj-1" {
		t.Fatalf("next job = %+v %v, want sj-1", acc, err)
	}
	calls := a.Calls()
	if len(calls) != 3 || calls[0] != (memory.Call{Op: "RecommendedInputPort", SubsystemID: "ASRS1"}) {
		t.Fatalf("calls = %+v", calls)
	}
}
