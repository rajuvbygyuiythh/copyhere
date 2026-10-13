package bot

import "testing"

func TestParseActiveMatchSocketShape(t *testing.T) {
	body := `{"inQueue":false,"match":{"conversation":{"id":"conv-1","participants":[{"profile":{"id":"me","username":"botme"}},{"profile":{"id":"u-9","username":"TexasM_35"}}]},"closure":{"closed":false}}}`
	st := parseActiveMatch(body, "me")
	if !st.HasConversation || st.Target.ConversationID != "conv-1" || st.Target.UserID != "u-9" || st.Target.Username != "TexasM_35" || st.Target.BotUsername != "botme" {
		t.Fatalf("bad parse: %+v", st)
	}
	if st.InQueue || !st.HasQueueState {
		t.Fatalf("queue state lost: %+v", st)
	}
}

func TestParseActiveMatchTopLevelConversation(t *testing.T) {
	body := `{"conversation":{"id":"conv-2","participants":[{"profile":{"id":"u-1","username":"guy"}},{"profile":{"id":"me","username":"botme"}}]}}`
	st := parseActiveMatch(body, "me")
	if !st.HasConversation || st.Target.ConversationID != "conv-2" || st.Target.UserID != "u-1" {
		t.Fatalf("bad parse: %+v", st)
	}
}

func TestParseActiveMatchFlatKeys(t *testing.T) {
	body := `{"conversationId":"conv-3","partnerId":"u-2","partnerUsername":"flatguy"}`
	st := parseActiveMatch(body, "me")
	if !st.HasConversation || st.Target.ConversationID != "conv-3" || st.Target.UserID != "u-2" || st.Target.Username != "flatguy" {
		t.Fatalf("bad parse: %+v", st)
	}
}

func TestParseActiveMatchInQueueOnly(t *testing.T) {
	st := parseActiveMatch(`{"inQueue":true}`, "me")
	if st.HasConversation || !st.InQueue || !st.HasQueueState {
		t.Fatalf("bad parse: %+v", st)
	}
}

func TestParseActiveMatchClosed(t *testing.T) {
	body := `{"inQueue":true,"match":{"conversation":{"id":"conv-4","participants":[{"profile":{"id":"me","username":"b"}},{"profile":{"id":"u-3","username":"gone"}}]},"closure":{"closed":true,"closedBy":"gone"}}}`
	st := parseActiveMatch(body, "me")
	if !st.HasConversation || !st.Closed || st.ClosedBy != "gone" || st.Target.ConversationID != "conv-4" {
		t.Fatalf("bad parse: %+v", st)
	}
}

func TestParseActiveMatchGarbage(t *testing.T) {
	for _, body := range []string{"", "null", "not json", `[]`, `{"inQueue":false}`} {
		if st := parseActiveMatch(body, "me"); st.HasConversation {
			t.Fatalf("%q: unexpected conversation %+v", body, st)
		}
	}
}
