Got it 👍 — thanks for clarifying.
You’re **not asking for full code**, you want a **clean, best-practice SKELETON** that shows *where things live and how they connect*, without over-implementation.

Below is a **battle-tested skeleton** that matches **Kong / Envoy / APISIX class of systems**, adapted to **Iket** and **your engine design**.

---

# 🧠 Design Principle (Before Skeleton)

**One rule governs everything:**

> **Runtime executes compiled artifacts.
> Control plane builds & validates them.**

So the skeleton must **enforce separation**, not just “look nice”.

---

# 📦 High-Level Package Skeleton (Best Practice)

```text
pkg/
├── core/
│   ├── compiler/          # config → executable plan
│   │   ├── compiler.go
│   │   ├── snapshot.go
│   │   ├── validator.go
│   │   ├── pipeline.go
│   │   └── errors.go
│   │
│   ├── engine/            # PURE execution runtime
│   │   ├── engine.go
│   │   ├── pipeline.go
│   │   ├── phase.go
│   │   ├── context.go
│   │   └── metrics.go
│   │
│   ├── runtime/           # orchestration glue
│   │   ├── gateway.go
│   │   ├── router.go
│   │   └── hotreload.go
│   │
│   └── audit/             # durable audit trail
│       ├── event.go
│       ├── emitter.go
│       └── sink/
│           ├── kafka.go
│           ├── file.go
│           └── noop.go
│
├── plugin/
│   ├── plugin.go          # base contracts
│   ├── registry.go        # compiler-only
│   ├── builtin/
│   │   ├── auth/
│   │   ├── opa/
│   │   ├── ratelimit/
│   │   └── logging/
│   └── wasm/              # future-proofing
│
├── config/
│   ├── provider/
│   │   ├── file/
│   │   ├── postgres/
│   │   └── provider.go
│   │
│   ├── snapshot.go
│   └── manager.go
│
├── observability/
│   ├── metrics/
│   ├── tracing/
│   └── logging/
│
└── api/
    ├── admin/             # control plane APIs
    └── public/            # gateway ingress
```

---

# 🔑 Responsibility Boundaries (Critical)

## 1️⃣ Config Manager (Source of Truth)

```text
file / db / git
        ↓
ConfigSnapshot (immutable)
```

* No execution logic
* No plugins
* No routing

---

## 2️⃣ Compiler (Brain)

```text
ConfigSnapshot
      ↓ validate
      ↓ resolve plugins
      ↓ map phases
      ↓ order
CompiledPipeline (immutable)
```

Skeleton:

```go
type Compiler interface {
	Compile(snapshot *ConfigSnapshot) (*CompiledGateway, error)
}
```

---

## 3️⃣ Engine (Muscle)

```text
CompiledPipeline
      ↓
Execute(ctx)
```

Skeleton:

```go
type Engine interface {
	Execute(ctx *ExecutionContext, pipeline CompiledPipeline) error
}
```

Engine **must not import**:

* config
* registry
* database

---

## 4️⃣ Runtime (Glue Layer)

This is where **real gateways differ from frameworks**.

```text
HTTP → Router → CompiledRoute → Engine
```

Skeleton:

```go
type GatewayRuntime struct {
	router   Router
	engine   engine.Engine
	compiler compiler.Compiler
}
```

---

## 5️⃣ Audit vs Observability (Separated by Design)

### Audit (Durable, Legal)

```go
type AuditEmitter interface {
	Emit(ctx context.Context, event AuditEvent)
}
```

Triggered ONLY at:

* auth decision
* policy decision
* request end

---

### Observability (Ephemeral, High Volume)

```go
Metrics → Prometheus
Traces  → OpenTelemetry
Logs    → stdout / ELK
```

Never mixed with audit.

---

# 🧱 Minimal Interface Contracts (Best Practice)

### Plugin (Compiler-time)

```go
type Plugin interface {
	Name() string
	New(config map[string]any) (PhasePlugin, error)
}
```

### PhasePlugin (Runtime)

```go
type PhasePlugin interface {
	Name() string
	Phase() Phase
	Priority() int
	Execute(ctx *ExecutionContext) error
	OnFailure() FailureMode
	Timeout() time.Duration
}
```

✔ Compiler instantiates
✔ Engine executes

---

# 🧩 Compiler Output Shape (Very Important)

```go
type CompiledGateway struct {
	Version string
	Routes  []CompiledRoute
}

type CompiledRoute struct {
	ID       string
	Match    RouteMatcher
	Pipeline engine.CompiledPipeline
}
```

Nothing else leaks into runtime.

---

# 🚨 Common Anti-Patterns (You Avoid These)

❌ Engine imports plugin registry
❌ Config validation in engine
❌ Plugin lookup at request time
❌ Audit = logs
❌ Metrics emitted as events

---

# ✅ What This Skeleton Gives You

✔ Hot reload with zero drift
✔ Multi-tenant ready
✔ DB + file config coexistence
✔ OPA / WASM friendly
✔ Enterprise audit compliance

This is **not over-engineered** — it’s the *minimum* needed to be competitive with Kong / Envoy.

---

# 🔜 Next Best Step (Choose One)

If you want, next I can:

1️⃣ Draw **mermaid diagram** of this skeleton
2️⃣ Show **ConfigSnapshot → CompiledPipeline flow**
3️⃣ Show **hot-reload lifecycle**
4️⃣ Show **OPA plugin placement in this skeleton**

Just say which one.
