package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/jorben/tacet/internal/kernel"
)

var ErrProtocolSession = errors.New("PROTOCOL_SESSION_INVALID")

// Transport is supplied by the supervisor. Closing must unblock reads and writes.
// Session never starts a process, resolves a path or creates a publish grant.
type Transport interface {
	io.Reader
	io.Writer
	io.Closer
}

type Session struct {
	transport  Transport
	schemas    *Schemas
	writeMu    sync.Mutex
	callMu     sync.Mutex
	incoming   chan sessionMessage
	done       chan struct{}
	once       sync.Once
	next       uint64
	generation uint64
	ready      bool
}

type sessionMessage struct {
	body json.RawMessage
	err  error
}
type rpcMessage struct {
	Version string          `json:"jsonrpc"`
	ID      uint64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

func NewSession(transport Transport) (*Session, error) {
	schemas, err := NewSchemas()
	if err != nil {
		return nil, err
	}
	for _, pair := range [][2]string{{"rpc.schema.json", "Message"}, {"initialize.schema.json", "Params"}, {"initialize.schema.json", "Result"}, {"stream.schema.json", "Item"}, {"stream.schema.json", "Credit"}, {"stream.schema.json", "End"}, {"lifecycle.schema.json", "Task"}, {"lifecycle.schema.json", "Cancel"}} {
		if err = schemas.Compile(pair[0], pair[1]); err != nil {
			return nil, err
		}
	}
	s := &Session{transport: transport, schemas: schemas, incoming: make(chan sessionMessage, 2), done: make(chan struct{}), next: 1}
	go func() {
		for {
			body, err := ReadFrame(transport)
			select {
			case s.incoming <- sessionMessage{body, err}:
			case <-s.done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return s, nil
}

func (s *Session) Close() error {
	var err error
	s.once.Do(func() { close(s.done); err = s.transport.Close() })
	return err
}

func (s *Session) send(message any, secrets []Secret) error {
	body, err := json.Marshal(message)
	if err != nil {
		return ErrProtocolSession
	}
	defer clear(body)
	if err = s.schemas.Validate("rpc.schema.json", "Message", body); err != nil {
		return err
	}
	if len(secrets) > 0 {
		// The frame with the secret is built last, in one preallocated buffer that
		// no marshaller, validator or logger ever sees, and is cleared after the write.
		frame, err := spliceSecrets(body, secrets)
		if err != nil {
			return err
		}
		defer clear(frame)
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		return WriteFrame(s.transport, frame)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return WriteFrame(s.transport, body)
}

// sendContext closes the transport on deadline so a plugin that stops reading
// cannot wedge the control lane indefinitely. Transport.Close must unblock both
// reads and writes, which the supervisor adapter is required to guarantee.
func (s *Session) sendContext(ctx context.Context, message any, secrets ...Secret) error {
	result := make(chan error, 1)
	go func() { result <- s.send(message, secrets) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		s.Close()
		<-result // closing the transport releases the writer and its secret buffer
		return ctx.Err()
	}
}

func (s *Session) receive(ctx context.Context) (rpcMessage, error) {
	select {
	case <-ctx.Done():
		return rpcMessage{}, ctx.Err()
	case <-s.done:
		return rpcMessage{}, ErrProtocolSession
	case value := <-s.incoming:
		if value.err != nil {
			return rpcMessage{}, ErrProtocolSession
		}
		defer clear(value.body)
		if err := s.schemas.Validate("rpc.schema.json", "Message", value.body); err != nil {
			return rpcMessage{}, err
		}
		var message rpcMessage
		if err := json.Unmarshal(value.body, &message); err != nil {
			return rpcMessage{}, ErrProtocolSession
		}
		return message, nil
	}
}

// Initialize binds plugin identity and ownership generation. Handshake failure
// permanently closes the transport; never guess an incompatible runtime.
func (s *Session) Initialize(ctx context.Context, pluginID, version string, generation uint64) error {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if s.ready || generation == 0 {
		return ErrProtocolSession
	}
	params := map[string]any{"protocol": map[string]string{"min": "1.0", "max": "1.0"}, "owner_generation": generation, "limits": map[string]int{"max_message_bytes": MaxMessageBytes}}
	id := s.next
	s.next++
	if err := s.sendContext(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": "initialize", "params": params}); err != nil {
		s.Close()
		return err
	}
	message, err := s.receive(ctx)
	if err != nil {
		s.Close()
		return err
	}
	if message.ID != id || len(message.Error) > 0 || s.schemas.Validate("initialize.schema.json", "Result", message.Result) != nil {
		s.Close()
		return ErrProtocolSession
	}
	var result struct {
		Protocol string `json:"protocol"`
		Plugin   struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"plugin"`
	}
	if err = json.Unmarshal(message.Result, &result); err != nil || result.Protocol != "1.0" || result.Plugin.ID != pluginID || result.Plugin.Version != version {
		s.Close()
		return ErrProtocolSession
	}
	s.generation = generation
	s.ready = true
	return nil
}

// StreamOutcome is still a stage result, never evidence of publish readiness.
type StreamOutcome struct {
	Status       string
	LastSequence uint64
	Code         string
}

// Stream commits one item at a time and replenishes its credit only after the
// supplied durable sink returns. The sink must enforce attempt/version checks
// and bounded storage and must honor ctx. Cancellation is sent on the control
// lane; supervisor owns deadline escalation and tree reclamation on failure.
func (s *Session) Stream(ctx context.Context, method string, attempt kernel.AttemptRef, params map[string]any, commit func(context.Context, json.RawMessage) error) (StreamOutcome, error) {
	return s.stream(ctx, method, attempt, params, nil, commit)
}

// StreamSecret is Stream for a call that carries secret values (the password of
// format.unlock). The secrets are added to the request after all validation, in
// a buffer the host clears, and the caller clears its own slices; they are never
// part of params, so they cannot pass through a map, a marshaller or the schema
// validator. The plugin validates the complete message against its own schema.
func (s *Session) StreamSecret(ctx context.Context, method string, attempt kernel.AttemptRef, params map[string]any, secrets []Secret, commit func(context.Context, json.RawMessage) error) (StreamOutcome, error) {
	if len(secrets) == 0 {
		return StreamOutcome{}, ErrProtocolSession
	}
	for _, secret := range secrets {
		// Refused before a request id is spent or anything is sent: a failure inside
		// the send path would close the whole session.
		if _, taken := params[secret.Field]; taken || secret.check() != nil {
			return StreamOutcome{}, ErrProtocolSession
		}
	}
	return s.stream(ctx, method, attempt, params, secrets, commit)
}

func (s *Session) stream(ctx context.Context, method string, attempt kernel.AttemptRef, params map[string]any, secrets []Secret, commit func(context.Context, json.RawMessage) error) (StreamOutcome, error) {
	s.callMu.Lock()
	defer s.callMu.Unlock()
	if !s.ready || attempt.OwnerGeneration != s.generation || attempt.JobID == "" || attempt.AttemptID == "" || attempt.JobVersion == 0 || commit == nil {
		return StreamOutcome{}, ErrProtocolSession
	}
	id := s.next
	s.next++
	streamID := attempt.AttemptID
	payload := make(map[string]any, len(params)+8)
	for key, value := range params {
		payload[key] = value
	}
	deadline := int64(600000)
	if until, ok := ctx.Deadline(); ok {
		deadline = time.Until(until).Milliseconds()
	}
	if deadline <= 0 || deadline > 86400000 {
		return StreamOutcome{}, ErrProtocolSession
	}
	payload["protocol"] = "1.0"
	payload["job_id"] = attempt.JobID
	payload["attempt_id"] = attempt.AttemptID
	payload["owner_generation"] = attempt.OwnerGeneration
	payload["job_version"] = attempt.JobVersion
	payload["deadline_ms"] = deadline
	payload["stream"] = map[string]any{"id": streamID, "credit": map[string]int{"bytes": 1024 * 1024, "items": 1}}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return StreamOutcome{}, ErrProtocolSession
	}
	err = s.schemas.Validate("lifecycle.schema.json", "Task", encoded)
	clear(encoded)
	if err != nil {
		return StreamOutcome{}, err
	}
	if err = s.sendContext(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": payload}, secrets...); err != nil {
		s.Close()
		return StreamOutcome{}, err
	}
	flow, _ := NewFlow(1024*1024, 1)
	for {
		message, err := s.receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				// The task context is already cancelled. Give the control write a
				// separate short deadline; a dead peer must not block tree reclaim.
				cancelCtx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				_ = s.sendContext(cancelCtx, map[string]any{"jsonrpc": "2.0", "method": "task.cancel", "params": map[string]any{"job_id": attempt.JobID, "attempt_id": attempt.AttemptID, "owner_generation": attempt.OwnerGeneration, "job_version": attempt.JobVersion}})
				cancel()
			}
			s.Close()
			return StreamOutcome{}, err
		}
		if message.Method == "stream.item" {
			if s.schemas.Validate("stream.schema.json", "Item", message.Params) != nil {
				s.Close()
				return StreamOutcome{}, ErrProtocolSchema
			}
			var item struct {
				Stream   string          `json:"stream"`
				Sequence uint64          `json:"seq"`
				Item     json.RawMessage `json:"item"`
			}
			if json.Unmarshal(message.Params, &item) != nil || item.Stream != streamID {
				s.Close()
				return StreamOutcome{}, ErrProtocolSession
			}
			// Compact representation matches SDK credit accounting; incoming
			// whitespace is not a way to claim more downstream queue capacity.
			var compact bytes.Buffer
			if err = json.Compact(&compact, item.Item); err != nil {
				s.Close()
				return StreamOutcome{}, ErrProtocolSession
			}
			size := uint64(compact.Len())
			if err = flow.Accept(item.Sequence, size); err != nil {
				s.Close()
				return StreamOutcome{}, err
			}
			err = commit(ctx, item.Item)
			clear(item.Item)
			clear(compact.Bytes())
			if err != nil {
				s.Close()
				return StreamOutcome{}, err
			}
			if err = flow.Commit(item.Sequence); err != nil {
				s.Close()
				return StreamOutcome{}, err
			}
			if err = flow.Credit(size, 1); err != nil {
				s.Close()
				return StreamOutcome{}, err
			}
			if err = s.sendContext(ctx, map[string]any{"jsonrpc": "2.0", "method": "stream.credit", "params": map[string]any{"stream": streamID, "committed_seq": item.Sequence, "bytes": size, "items": 1}}); err != nil {
				s.Close()
				return StreamOutcome{}, err
			}
			continue
		}
		if message.ID != id || len(message.Error) > 0 || s.schemas.Validate("stream.schema.json", "End", message.Result) != nil {
			s.Close()
			return StreamOutcome{}, ErrProtocolSession
		}
		var end struct {
			Stream string            `json:"stream"`
			Status string            `json:"status"`
			Last   uint64            `json:"last_seq"`
			Error  *kernel.ErrorCode `json:"error"`
		}
		if json.Unmarshal(message.Result, &end) != nil || end.Stream != streamID || flow.End(end.Last) != nil {
			s.Close()
			return StreamOutcome{}, ErrProtocolSession
		}
		out := StreamOutcome{Status: end.Status, LastSequence: end.Last}
		if end.Error != nil {
			out.Code = end.Error.Code
		}
		return out, nil
	}
}
