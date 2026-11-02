# Rate limit

A small Go rate limiter that uses the fixed-window counter algorithm and stores counters in Redis. Several app processes share one limit because the count lives in Redis, not in memory.

The demo server rejects extra requests. It does not slow them down.

## How it works

Time is split into back-to-back windows of a fixed length. Each client has one counter per window. A request increments that counter. When the count passes the limit, further requests in that window are rejected. The next window uses a new Redis key, so the count starts at zero again.

A window of 1 minute and a limit of 10 means the same client can call `GET /hello` 10 times between 12:00:00 and 12:01:00. Request 11 gets `429` until 12:01:00.

The Redis key is `ratelimit:<client>:<window start>`. Increment and expiry run in one Lua script, so a crash cannot leave a counter that never expires. The key is removed when the window ends.

Near a window boundary, the end of one window and the start of the next can sit one second apart. A client can briefly send about twice the limit.

## Layout

```
limiter.go            fixed-window limiter
limiter_test.go       tests, using an in-memory Redis
cmd/server/main.go    demo HTTP server
docker-compose.yml    Redis and the demo server
Dockerfile            builds the demo server
```

## Run the demo

Docker Compose starts Redis, waits until it answers `PING`, then starts the server.

```bash
docker compose up --build
```

The server listens on [http://localhost:8080](http://localhost:8080). The default limit is 10 requests per minute.

```bash
curl -i localhost:8080/hello
curl -i localhost:8080/healthz
```

`GET /hello` is rate limited. `GET /healthz` only checks Redis and is not counted.

Send `X-API-Key` to give a client its own counter. With no header, the client IP is the counter.

```bash
curl -i -H 'X-API-Key: alice' localhost:8080/hello
```

Call `/hello` until the limit is passed. The next response is `429 Too Many Requests` with these headers:

| Header | Meaning |
|---|---|
| `X-RateLimit-Limit` | Requests allowed in the current window |
| `X-RateLimit-Remaining` | Requests left in the current window |
| `X-RateLimit-Reset` | Unix time when the window ends |
| `Retry-After` | Seconds to wait, set only on `429` |

If Redis cannot be reached, `/hello` returns `503`.

## Configuration

Set these on the demo server. Compose already sets the first three.

| Variable | Default | Meaning |
|---|---|---|
| `ADDR` | `:8080` | Listen address |
| `REDIS_ADDR` | `localhost:6379` | Redis address |
| `REDIS_PASSWORD` | empty | Redis password |
| `RATE_LIMIT` | `10` | Requests allowed per window. Must be at least 1 |
| `WINDOW` | `1m` | Window length, parsed as a Go duration (`10s`, `1m`, `1h`). Must be at least `1ms` |

## Use the library

```go
rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379"})

limiter, err := ratelimit.New(rdb, 10, time.Minute)
if err != nil {
    log.Fatal(err)
}

decision, err := limiter.Allow(ctx, userID)
if err != nil {
    // Redis failed; the request was not counted
}
if !decision.Allowed {
    // reject until decision.Reset
}

fmt.Println(decision.Limit, decision.Remaining, decision.Reset)
```

`key` is any stable identity, such as a user id or an API key. An empty key returns an error.

## Tests

The tests run against an in-memory Redis, so a real Redis server is not required.

```bash
go test ./...
```

Go 1.23 or newer.
