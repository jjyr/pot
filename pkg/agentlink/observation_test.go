package agentlink

import (
	"context"
	"testing"
)

func TestObservationValidationAndAuthenticatedSampling(t *testing.T) {
	a, b := newFixture(t, "observe-a"), newFixture(t, "observe-b")
	a.service.options.Backend = &testBackend{f: a}
	b.service.options.Backend = &testBackend{f: b}
	c := connect(t, a, b)
	if err := a.service.Sync(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	sample := a.service.Observation(c.ID)
	if sample.Value == nil || sample.Value.NodeID != "observe-b" || sample.ObservedAtMS <= 0 || len(sample.Value.Assistants) != 2 {
		t.Fatalf("missing live peer sample: %+v", sample)
	}
	sample.Value.Assistants[0].Running = 99
	if a.service.Observation(c.ID).Value.Assistants[0].Running != 0 {
		t.Fatal("cache escaped through returned slice")
	}
	b.service.options.Backend.(*testBackend).observationFailed.Store(true)
	if err := a.service.Sync(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if failed := a.service.Observation(c.ID); failed.Error == "" || failed.Value == nil || failed.ObservedAtMS != sample.ObservedAtMS {
		t.Fatalf("failed observation lost history or fabricated freshness: %+v", failed)
	}
	b.service.options.Backend.(*testBackend).observationFailed.Store(false)
	directory, err := b.service.localDirectory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	valid := *a.service.Observation(c.ID).Value
	for _, alter := range []func(*Observation){
		func(o *Observation) { o.NodeID = "other" },
		func(o *Observation) { o.Assistants = o.Assistants[:1] },
		func(o *Observation) { o.Assistants[1].AssistantID = o.Assistants[0].AssistantID },
		func(o *Observation) { o.Assistants[0].Running = -1 },
		func(o *Observation) { o.Assistants[0].ChatState = "made-up" },
		func(o *Observation) { o.Outbox.Pending = -1 },
	} {
		copy := valid
		copy.Assistants = append([]Activity(nil), valid.Assistants...)
		alter(&copy)
		if validateObservation(copy, directory) == nil {
			t.Fatalf("accepted invalid observation: %+v", copy)
		}
	}
	if err = a.service.Disconnect(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	if sample = a.service.Observation(c.ID); sample.Value == nil {
		t.Fatal("disconnect discarded last known sample")
	}
}
