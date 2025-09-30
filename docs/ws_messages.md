# WebSocket Message Contracts

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

### BALANCE_UPDATED
Payload schema:
```json
{
  "balance": 12345
}
```
Delivered to user-specific subscriptions when ledger operations change balance.

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
- `streamer:{streamerId}` — receives EVENT_* updates.
- `game:{gameId}` — narrower scope for specific games.
- `user:{userId}` — balance updates and personal notices.

Clients may send subscription commands over the socket:
```json
{ "action": "subscribe", "channels": ["streamer:<uuid>"] }
```
Unsubscribe works similarly with `"action":"unsubscribe"`.

## Backpressure Strategy
- Server enforces max 2–4 EVENT_UPDATED per second per channel by aggregating totals.
- If downstream is slow, server drops intermediate EVENT_UPDATED but always sends latest snapshot and EVENT_CLOSED.
- Heartbeat/ping every 20 seconds; clients must respond with `pong`.

