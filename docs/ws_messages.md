# WebSocket Message Contracts

> Status: this document defines **message contracts only**. Runtime WebSocket delivery in backend code must be implemented separately.

Clients connect to `wss://<host>/realtime?streamerId=...` (optionally multiple subscriptions negotiated in payload) using the JWT issued by `/api/auth`. Messages are JSON objects with the following shape:

```json
{
  "type": "EVENT_CREATED",
  "payload": { ... }
}
```

## Message Types

### EVENT_CREATED
Payload schema:
```json
{
  "event": {
    "id": "uuid",
    "streamerId": "uuid",
    "gameId": "uuid|null",
    "title": "string",
    "options": [ { "id": "string", "label": "string" } ],
    "closesAt": "ISO-8601",
    "costPerVote": 100,
    "promptVersions": { "session": "v12", "game": "v3", "perClip": "v7" }
  }
}
```
Sent when a new live event is accepted from the worker.

### EVENT_UPDATED
Payload schema:
```json
{
  "eventId": "uuid",
  "totals": { "optionId": 123 },
  "closesAt": "ISO-8601|null"
}
```
Broadcast every 250–500 ms at most, optionally batching multiple events per frame.

### EVENT_VOTE_FEED_UPDATED
Payload schema:
```json
{
  "eventId": "uuid",
  "items": [
    {
      "userId": "uuid",
      "nickname": "BraveFox123",
      "optionId": "ct",
      "amountINT": 150,
      "createdAt": "ISO-8601"
    }
  ],
  "snapshotAt": "ISO-8601"
}
```
Real-time feed of participant votes for the mini-game. Frontend should append `items` to the vote tape and reconcile by `eventId + userId + createdAt`.

### EVENT_CLOSED
Payload schema:
```json
{
  "eventId": "uuid",
  "result": {
    "optionId": "string",
    "totals": { "optionId": 456 }
  }
}
```
Sent when the event timer expires or the event is manually closed.

### SCENARIO_STEP_UPDATED
Payload schema:
```json
{
  "streamerId": "uuid",
  "scenarioId": "uuid",
  "stepId": "string",
  "stepName": "Fight for Mid",
  "updatedAt": "ISO-8601"
}
```
Broadcast whenever orchestration moves to the next scenario step. For end users only the step name must be rendered.

### LLM_MATCH_STATE_UPDATED
Payload schema:
```json
{
  "streamerId": "uuid",
  "matchSessionId": "uuid",
  "gameKey": "cs2",
  "status": "in_progress",
  "stateSummary": {
    "mode": "competitive",
    "playerOutcome": "unknown",
    "score": { "ct": 7, "t": 5 }
  },
  "confidence": 0.88,
  "ts": "ISO-8601"
}
```
Sent when the LLM state tracker persists a new match-state snapshot for a streamer.

### LLM_MATCH_FINALIZED
Payload schema:
```json
{
  "streamerId": "uuid",
  "matchSessionId": "uuid",
  "outcome": "win",
  "finalScore": { "ct": 13, "t": 10 },
  "confidence": 0.97,
  "ts": "ISO-8601"
}
```
Sent when the backend finalizes a match-session outcome from accumulated evidence.

### BALANCE_UPDATED
Payload schema:
```json
{
  "balance": 12345
}
```
Delivered to user-specific subscriptions when ledger operations change balance.

### USER_BET_UPDATED
Payload schema:
```json
{
  "eventId": "uuid",
  "myBetTotalINT": 150,
  "myOptionId": "ct",
  "myCoefficient": 1.42,
  "myPotentialWinINT": 213,
  "updatedAt": "ISO-8601"
}
```
Delivered to user-specific subscriptions right after successful vote processing.

### SYSTEM_NOTICE
Payload schema:
```json
{
  "code": "string",
  "message": "string"
}
```
Used for rate-limit warnings, maintenance messages, or feature flag updates.

## Subscriptions
- `streamer:{streamerId}` — receives EVENT_* updates plus scenario-step and LLM match-state updates.
- `game:{gameId}` — narrower scope for specific games.
- `user:{userId}` — balance updates and personal notices.

Clients may send subscription commands over the socket:
```json
{ "action": "subscribe", "channels": ["streamer:<uuid>"] }
```
Unsubscribe works similarly with `"action":"unsubscribe"`.

## Backpressure Strategy
- Server enforces max 2–4 EVENT_UPDATED per second per channel by aggregating totals.
- `EVENT_VOTE_FEED_UPDATED` is sampled to max 5–10 frames per second with batch size control; slow subscribers receive only newest batches.
- If downstream is slow, server drops intermediate EVENT_UPDATED but always sends latest snapshot and EVENT_CLOSED.
- Heartbeat/ping every 20 seconds; clients must respond with `pong`.
