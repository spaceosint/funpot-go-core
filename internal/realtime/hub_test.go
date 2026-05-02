package realtime

import "testing"

func TestHubPublishesToUserSubscribers(t *testing.T) {
	hub := NewHub()
	ch, unsubscribe := hub.SubscribeUser("u1", 1)
	defer unsubscribe()

	hub.PublishToUser("u1", Envelope{Type: "BALANCE_UPDATED", Payload: map[string]int64{"balance": 90}})

	select {
	case env := <-ch:
		if env.Type != "BALANCE_UPDATED" {
			t.Fatalf("unexpected type: %s", env.Type)
		}
	default:
		t.Fatal("expected user envelope")
	}
}
