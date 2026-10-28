# Task API (Go + SQLite)

A small REST API in Go with SQLite storage, a goroutine worker-pool endpoint, and an optional LLM endpoint.
Standard library for HTTP and JSON; the only dependency is a pure-Go SQLite driver (no C compiler needed).

## Run it

```bash
go mod tidy          # fetches dependencies (first time only)
go run .             # starts on http://localhost:8080
go test -race ./...  # runs all tests
```

Settings come from environment variables (all optional):

| Variable | Default | Meaning |
|---|---|---|
| `PORT` | `8080` | HTTP port |
| `DB_PATH` | `tasks.db` | SQLite file |
| `LLM_API_KEY` | none | Enables `/ai/chat` when set |
| `LLM_BASE_URL` | `https://api.x.ai/v1` | Any OpenAI-compatible provider |
| `LLM_MODEL` | `grok-4.3` | Model name for that provider |

## Endpoints

| Method | Path | What it does |
|---|---|---|
| GET | `/health` | Database check and whether the LLM is configured |
| POST | `/tasks` | Create a task (`title` required, `description`, `status` optional) |
| GET | `/tasks?status=todo` | List tasks, optionally filtered by status |
| GET | `/tasks/{id}` | Get one task |
| PUT | `/tasks/{id}` | Update a task |
| DELETE | `/tasks/{id}` | Delete a task |
| POST | `/tasks/process` | Mark tasks done using 4 worker goroutines (`{"ids":[1,2]}`, or empty body for all `todo` tasks) |
| POST | `/ai/chat` | Send `{"prompt":"..."}` to the LLM, get `{"reply":"..."}` |

Statuses: `todo`, `in_progress`, `done`.

## Try it

```bash
curl -s -X POST localhost:8080/tasks -d '{"title":"first task","description":"hello"}'
curl -s localhost:8080/tasks
curl -s -X PUT localhost:8080/tasks/1 -d '{"title":"first task","status":"in_progress"}'
curl -s -X POST localhost:8080/tasks/process
curl -s -X DELETE -i localhost:8080/tasks/1

# needs LLM_API_KEY in the terminal that runs the server
curl -s -X POST localhost:8080/ai/chat -d '{"prompt":"say hi in five words"}'
```

## Structure

```
main.go        start-up, settings, graceful shutdown
models/        Task type and input validation
store/         all SQL (SQLite)
handlers/      routes, JSON in/out, status codes, middleware
llm/           small OpenAI-compatible chat client
```

## Design notes

- Layers are separate: handlers never write SQL, the store never knows about HTTP.
- Input is validated before it reaches the database. Unknown JSON fields and oversized bodies get a 400.
- Errors map to the right status: 400 bad input, 404 missing, 502 provider failure, 503 not configured.
- `/tasks/process` uses a channel of jobs, a fixed pool of workers, and a `WaitGroup`. Each worker writes to its own slot of the results slice, so the race detector stays clean.
- SQLite uses one connection plus WAL and a busy timeout, which avoids "database is locked" errors.
- The API key is read from the environment only and is never logged.

## Assumptions and next steps

- Assumed a single-user app with no authentication.
- Next: pagination on `/tasks`, auth, request IDs in logs, a Dockerfile, and migrations if the schema grows.
