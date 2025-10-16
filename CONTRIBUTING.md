# Contributing to Twimulator

Thank you for your interest in contributing to Twimulator!

## Development Setup

1. **Prerequisites**
   - Go 1.22 or later
   - No other dependencies required (stdlib only!)

2. **Clone and Build**
   ```bash
   git clone <repository-url>
   cd twimulator
   go build ./...
   ```

3. **Run Tests**
   ```bash
   go test ./...
   go test ./... -race  # Check for race conditions
   ```

4. **Run the Demo**
   ```bash
   go run examples/console_demo.go
   # Visit http://localhost:8089
   ```

## Project Structure

```
twimulator/
├── model/          # Core data types (Call, Queue, Conference, Event)
│   └── core.go     # SID generators, status enums
├── twiml/          # TwiML parsing
│   ├── ast.go      # AST node definitions
│   └── parser.go   # XML parser
├── engine/         # Core engine
│   ├── clock.go    # Time control (Manual/Auto)
│   ├── engine.go   # Main engine implementation
│   └── runner.go   # Per-call TwiML executor
├── httpstub/       # HTTP client abstraction
│   └── webhook.go  # Real and mock implementations
├── twilioapi/      # Optional Twilio-like API
│   └── api.go      # REST façade
├── console/        # Web UI
│   ├── server.go   # HTTP server
│   ├── templates/  # HTML templates
│   └── static/     # CSS files
└── examples/       # Demo programs
    └── console_demo.go
```

## Adding a New TwiML Verb

1. **Define the AST Node** (`twiml/ast.go`):
   ```go
   type MyVerb struct {
       Attribute string
       Children  []Node
   }

   func (MyVerb) isNode() {}
   ```

2. **Add Parser Support** (`twiml/parser.go`):
   ```go
   case "MyVerb":
       return parseMyVerb(decoder, start)
   ```

3. **Implement Execution** (`engine/runner.go`):
   ```go
   func (r *CallRunner) executeMyVerb(ctx context.Context, verb *twiml.MyVerb) error {
       r.addEvent("twiml.myverb", map[string]any{
           "attribute": verb.Attribute,
       })
       // Execute logic here
       return nil
   }
   ```

4. **Add Tests** (`twiml/parser_test.go` and `engine/engine_test.go`):
   ```go
   func TestParseMyVerb(t *testing.T) {
       xml := `<Response><MyVerb attribute="value"/></Response>`
       resp, err := Parse([]byte(xml))
       // Assertions...
   }
   ```

5. **Update Documentation** (`README.md`):
   - Add to supported verbs list
   - Provide usage example

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Use meaningful variable names
- Add comments for exported types and functions
- Keep functions focused and reasonably sized
- Prefer composition over inheritance

## Testing Guidelines

### Unit Tests
- Use table-driven tests when appropriate
- Test edge cases and error conditions
- Mock external dependencies (use `httpstub.MockWebhookClient`)

### Integration Tests
- Use `httptest.Server` for real HTTP interactions
- Use `ManualClock` for deterministic timing
- Add small `time.Sleep()` calls to let goroutines schedule:
  ```go
  time.Sleep(10 * time.Millisecond)
  e.Advance(duration)
  time.Sleep(10 * time.Millisecond)
  ```

### Running Tests
```bash
# All tests
go test ./...

# Specific package
go test ./twiml -v

# With race detector
go test ./... -race

# With coverage
go test ./... -cover
```

## Submitting Changes

1. **Fork the Repository**
2. **Create a Feature Branch**
   ```bash
   git checkout -b feature/my-new-feature
   ```

3. **Make Your Changes**
   - Write code
   - Add tests
   - Update documentation

4. **Run Tests**
   ```bash
   go test ./...
   go test ./... -race
   ```

5. **Commit with Clear Messages**
   ```bash
   git commit -m "Add support for <Record> verb"
   ```

6. **Push and Create PR**
   ```bash
   git push origin feature/my-new-feature
   ```

## Ideas for Contributions

### Easy
- Add more TwiML verb attributes
- Improve error messages
- Add more test cases
- Fix typos in documentation

### Medium
- Add `<Record>` verb
- Implement `<Refer>` for transfers
- Add queue position tracking
- Improve conference management

### Hard
- Full child call leg simulation
- Performance optimizations
- Advanced queue routing logic
- SIP address support

## Questions?

- Open an issue for discussion
- Check existing issues for similar questions
- Review the README and SUMMARY for architecture details

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
