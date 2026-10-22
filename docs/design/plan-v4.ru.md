# foreman-code-ops — план реализации (v4)

Open-source MCP-сервер и плагин для Claude Code. Claude (Opus) планирует задачу и выбирает символы. Локальная или дешёвая модель пишет код. Линтер, сборка и опционально облачный валидатор проверяют результат. Сервер вставляет код через tree-sitter и коммитит каждое изменение.

**Решения, принятые после уточнений**

| Вопрос | Решение |
|---|---|
| Граф кода | абстракция `GraphProvider`; поддерживаются `codebase-memory-mcp` и `graphify` (§9) |
| Лицензия | MIT |
| Название | `foreman-code-ops` (§0) |
| Платформы | Linux — цель v1; macOS — best effort (CI-прогон, без гарантий); Windows — не в v1 |
| Валидатор по умолчанию | `z-ai/glm-5.3-flash` через OpenRouter; batch — исследовать (§7a) |
| Документация | английский + русский перевод; общение Claude с MCP только на английском |
| Монорепо | `.foreman/workspace.yaml` с корнями сервисов + обнаружение связей между ними (§11a) |
| Ветка | коммиты в текущую ветку |
| Языки v1 | Go, Python, TypeScript/JavaScript, TSX/JSX |
| Модели | генератор и валидатор настраиваются раздельно (провайдер + модель); валидатор можно отключить |
| Локальная модель | на той же машине; бюджет 3/4 от 256k = 192k токенов, делится на несколько слотов |
| Гейт | только формат + импорты + линт + сборка; тесты — по запросу Claude |
| Git | коммит после каждой успешной операции |
| Стиль проекта | `.foreman/style.md`, создаёт и обновляет Claude через MCP |
| Распространение | open source: generic-адаптеры на YAML с первого этапа, документация, CI, релизы |

---

## 0. Название

**`foreman-code-ops`**. Foreman — «прораб»: раздаёт работу исполнителю и принимает её.

| Где | Значение |
|---|---|
| Репозиторий / Go-модуль | `github.com/MarshMeg/foreman-code-ops` |
| Бинарник | `foreman-code-ops` (короткий алиас в документации: `foreman`) |
| Имя MCP-сервера в `.mcp.json` | `foreman` (инструменты: `mcp__foreman__upsert_symbol` и т. д.) |
| Папка проекта | `.foreman/` (`config.yaml`, `style.md`, `workspace.yaml`, `links.json`, `cache/`) |
| Глобальный конфиг | `~/.config/foreman-code-ops/config.yaml` |
| Переменные окружения | префикс `FOREMAN_` |
| Git-трейлеры | `Foreman-Written-By`, `Foreman-Reviewed-By`, `Foreman-Attempts` |

### Отложено

- Шаблон `.foreman/style.md`: составить позже на реальном Go-проекте.

---

## 1. Цели и границы

**Цели v1**
- Создание, замена и удаление функций, методов, типов и классов через MCP вместо `Edit`/`Write`.
- Генерация кода моделью-исполнителем (локальной или облачной, через конфиг).
- Проверка: формат → импорты → линт/сборка → валидатор (если включён).
- До 2 кругов «замечания → исправление», затем фолбэк.
- Ответ генератора: код + опциональный список импортов.
- Автокоммит каждой операции.
- Адаптеры Go, Python, TS/JS, TSX/JSX.

**Не цели v1**
- Собственный граф кода (используем внешний).
- Межфайловые рефакторинги (rename по проекту) — v2 через LSP.
- Генерация тестов.

---

## 2. Архитектура

```
Пользователь ↔ Claude Code (Opus 5.5)
                 ├── codebase-memory-mcp: граф, символы, вызовы, impact
                 └── foreman-code-ops MCP (Go, один бинарник)
                       ├── core
                       │   ├── MCP-инструменты
                       │   ├── pipeline: generate → gate → review → fallback → apply → commit
                       │   ├── scheduler: очередь задач по слотам генератора
                       │   ├── context budget: укладка контекста в окно слота
                       │   ├── workspace: temp-копии, diff, атомарная запись, откат
                       │   ├── git: stage только затронутых файлов, коммит с трейлерами
                       │   ├── style: .foreman/style.md
                       │   ├── graph: GraphProvider (переиндексация после коммита)
                       │   ├── providers: openai-compatible | openrouter | anthropic | agent-sdk | none
                       │   └── journal: лог попыток и метрики
                       └── adapters/<lang> (YAML + .scm + prompt.md)
```

**Стек:** Go, официальный Go SDK для MCP, биндинги tree-sitter для Go (cgo), YAML-конфиги адаптеров. Релизы собираются нативно на раннере каждой платформы (cgo исключает простую кросс-компиляцию; goreleaser OSS не умеет склеивать такие сборки).

---

## 3. Структура репозитория

```
foreman-code-ops/
├── cmd/foreman-code-ops/main.go
├── internal/
│   ├── mcp/              # инструменты, схемы входа/выхода
│   ├── pipeline/
│   ├── scheduler/        # семафор по слотам, приоритеты
│   ├── budget/           # подсчёт токенов, обрезка контекста
│   ├── workspace/
│   ├── edit/             # правка по байтовым диапазонам
│   ├── gitops/
│   ├── style/
│   ├── graph/            # GraphProvider + codebase-memory, noop
│   ├── providers/
│   │   ├── openai.go     # OpenAI-compatible (llama-server, OpenRouter, любые)
│   │   ├── anthropic.go  # Messages API напрямую
│   │   ├── agentsdk.go   # через sidecar (см. §8)
│   │   └── schemas/
│   ├── lang/{adapter.go, registry.go, generic/}
│   └── journal/
├── adapters/
│   ├── go/{adapter.yaml, symbols.scm, imports.scm, prompt.md}
│   ├── python/...
│   ├── typescript/...    # .ts, .mts, .cts
│   ├── tsx/...           # .tsx, .jsx (отдельная грамматика tsx)
│   └── javascript/...    # .js, .mjs, .cjs
├── prompts/{generate.md, fix.md, review.md, fallback.md}
├── sidecar/agent-sdk/    # маленький Node-процесс для Agent SDK (опционально)
├── plugin/
│   ├── .claude-plugin/plugin.json
│   ├── .mcp.json
│   ├── hooks/hooks.json
│   └── skills/foreman-code-ops/SKILL.md
├── docs/{README.md, config.md, adapters.md, providers.md}
├── testdata/{go-layered, py-fastapi, ts-express, tsx-react}
├── .github/workflows/{ci.yml, release.yml}
├── LICENSE               # MIT
└── config.example.yaml
```

---

## 4. MCP-инструменты

### `upsert_symbol`
Создать или заменить символ. Если файла нет, он создаётся.

| Поле | Тип | Описание |
|---|---|---|
| `file` | string | путь от корня проекта |
| `symbol` | string | `BookService.List`, `BookRepo.list`, `BookList` (компонент) |
| `kind` | enum | `function` \| `method` \| `type` \| `class` \| `component` \| `interface` |
| `spec` | string | что делает |
| `interface` | string | сигнатура, типы, ошибки, пропсы |
| `context` | `[{file, symbol}]` | ссылки на символы; текст достаёт сервер |
| `anchor` | string? | вставить после этого символа (для новых) |
| `mode` | enum? | `local` (по умолчанию) \| `direct` (сразу валидатор) |
| `package` | string? | для нового Go-файла |
| `run_tests` | `string \| bool`? | запустить тесты; `true` = тесты пакета/модуля, строка = фильтр |
| `commit_message` | string? | иначе генерируется автоматически |
| `limits` | object? | переопределение лимитов цикла для этого вызова (§7) |

**Ответ**
```json
{
  "status": "applied | failed",
  "symbol": "BookService.List",
  "signature": "func (s *BookService) List(ctx context.Context, f BookFilter) ([]Book, error)",
  "diff": "...",
  "commit": "a1b2c3d",
  "written_by": "local | validator | none",
  "attempts": { "build_fixes": 1, "reviews": 2 },
  "minor_notes": [],
  "diagnostics": [],
  "tests": { "ran": false }
}
```

### Остальные инструменты

| Инструмент | Вход | Назначение |
|---|---|---|
| `get_symbol` | `{file, symbol}` | текст и сигнатура символа |
| `delete_symbol` | `{file, symbol, force?}` | удаление; без `force` проверяет использования через граф |
| `create_file` / `delete_file` | `{path, package?}` | явные файловые операции (тоже с коммитом) |
| `check` | `{paths?}` | прогон гейта без изменений |
| `run_tests` | `{target?, filter?}` | тесты по запросу Claude |
| `style_get` | `{}` | текущий `.foreman/style.md` |
| `style_set` | `{content}` или `{section, content}` | создать/обновить стиль (коммитится) |
| `status` | `{}` | провайдеры, слоты, очередь, состояние графа |

---

## 5. Pipeline `upsert_symbol`

```
acquire slot (scheduler)
ctx     = resolve(context refs) + style.md + adapter.prompt
ctx     = budget.fit(ctx, slot_ctx - output_reserve)
current = adapter.find(file, symbol)
out     = generator.generate(task, ctx, current)        // {code, imports}

loop:
    ws = workspace.copy(touched files)
    ws.apply(file, symbol | anchor, out.code)
    adapter.insertImports(ws, file, out.imports)
    adapter.fixImports(ws, file)                        // goimports / ruff --fix / organize imports
    adapter.format(ws, file)
    diags = adapter.check(ws, file)

    if diags.errors:
        if build_fixes >= MAX_BUILD_FIXES: goto fallback
        out = generator.fix(out, diags); build_fixes++; continue

    if validator == none: break
    verdict = validator.review(task, ctx, out.code, diff)
    if verdict.ok or verdict.severity == "minor": break
    if reviews >= MAX_REVIEWS: goto fallback
    out = generator.fix(out, verdict.issues); reviews++

if run_tests: tests = adapter.test(ws, target)          // результат возвращается Claude, не блокирует
commit(ws) → git commit → graph.reindex(files)
return

fallback:
    validator != none → validator.write(task, ctx, history), 1–2 попытки гейта
    validator == none → status=failed + диагностики + последний вариант кода (Claude решает сам)
```

**Правила**
- Реальный файл меняется только при `commit`: запись во временный файл и rename.
- Замечания `minor` не запускают новый круг.
- Если валидатор отключён, единственная проверка — гейт. В ответе это видно по `written_by` и отсутствию `review`.
- Тесты не входят в цикл исправлений: их результат возвращается Claude, он решает, что делать дальше.

---

## 6. Локальная модель: слоты и бюджет контекста

**Запуск llama-server** (пример: 3 слота по 64k):
```
llama-server -m qwythos-9b-q4_k_m.gguf -c 196608 -np 3 --port 8080 ...
```
В llama.cpp общий `-c` делится поровну между `-np` слотами. Варианты:

| Слотов | Контекст на слот | Когда |
|---|---|---|
| 1 | 192k | огромный контекст, строго последовательно |
| 2 | 96k | баланс |
| 3 | 64k | рекомендуемый старт: параллельная генерация независимых символов |
| 4 | 48k | много мелких задач |

**Что делает сервер**
- Слоты задаются в двух местах: в llama.cpp (`-c`, `-np`) и в конфиге MCP (`generator.slots`, `generator.slot_ctx`). Конфиг MCP — источник истины для планировщика и бюджета контекста.
- При старте и в `status` сервер сверяет конфиг с `/props` или `/slots` у llama-server. При расхождении пишет предупреждение и использует меньшее из значений, чтобы не переполнить слот и не держать лишние задачи в очереди.
- `scheduler` — семафор на N слотов. Задачи, которые зависят друг от друга (adapter после service), Claude и так вызывает последовательно. Независимые задачи идут параллельно.
- `budget.fit` укладывает контекст в окно слота с резервом под ответ (`output_reserve`, по умолчанию 8k). Порядок приоритета при обрезке:
  1. задача, интерфейс, текущий код символа;
  2. символы из `context`;
  3. `style.md`;
  4. сигнатуры соседних символов файла (без тел);
  5. импорты файла.
- Токены считаются через `/tokenize` llama-server. Для облачных провайдеров — приблизительная оценка.
- Если обязательная часть (пункт 1) не помещается, задача сразу уходит в фолбэк или возвращается Claude с ошибкой `context_too_large`.

---

## 7. Провайдеры моделей

Генератор и валидатор настраиваются независимо. Приоритет источников: аргументы CLI → переменные окружения → `config.yaml` проекта (`.foreman/config.yaml`) → глобальный конфиг (`~/.config/foreman-code-ops/config.yaml`).

```yaml
generator:
  provider: openai          # openai | openrouter | anthropic | agent-sdk
  base_url: http://127.0.0.1:8080/v1
  model: qwythos-9b-q4_k_m
  temperature: 0.2
  slots: 3                  # число слотов; должно совпадать с -np у llama-server
  slot_ctx: 65536           # контекст на слот; = -c / -np у llama-server
  output_reserve: 8192
  structured: json_schema   # json_schema | grammar | none

validator:
  provider: openrouter      # openai | openrouter | anthropic | agent-sdk | none
  model: z-ai/glm-5.3-flash    # по умолчанию; любая модель провайдера
  api_key_env: OPENROUTER_API_KEY
  timeout: 120s
  batch:
    enabled: false            # см. §7a

limits:
  max_build_fixes: 3        # исправлений локальной моделью по ошибкам сборки/линта
  max_reviews: 2            # кругов «замечания валидатора → исправление»
  max_fallback_attempts: 2  # попыток гейта для кода, написанного валидатором
  max_parallel_reviews: 8   # одновременных запросов к валидатору
  generate_timeout: 300s
  check_timeout: 120s
  test_timeout: 600s
```

Все лимиты можно переопределить на уровне проекта (`.foreman/config.yaml`), через CLI (`--max-reviews 3`) и для одного вызова: `upsert_symbol` принимает необязательное поле `limits` с теми же ключами. Его использует Claude для заведомо сложных задач.

CLI-эквиваленты: `--generator-provider`, `--generator-model`, `--validator-provider none` и т. д.

| Провайдер | Как реализован | Structured output |
|---|---|---|
| `openai` | OpenAI-compatible HTTP (llama-server, vLLM, LM Studio) | `response_format: json_schema` / GBNF |
| `openrouter` | тот же клиент + заголовки OpenRouter | `response_format`, если модель поддерживает; иначе tool-call |
| `anthropic` | Messages API напрямую | через tool use с `input_schema` |
| `agent-sdk` | sidecar на Node (§8) | через инструкции + проверку схемы на стороне сервера |
| `none` | валидатор отключён | — |

На ответ любого провайдера накладывается одна защита: валидация по JSON Schema на стороне сервера и один повтор с текстом ошибки, если ответ не прошёл проверку.

---

## 7a. Валидатор по умолчанию и batch

**Модель:** `z-ai/glm-5.3-flash` через OpenRouter. Это мультимодальная модель Z.ai с поддержкой tools и structured outputs; цена порядка $0.075 за 1M входных и $0.25 за 1M выходных токенов, контекст около 1M. Для ревью одной функции это доли цента. Если важна скорость, есть вариант `z-ai/glm-5.3-flashx` (до ~200 ток/с).

**Batch — исследовать на этапе 4, в v1 не закладывать в основной путь.**
- У OpenRouter, насколько известно, нет batch API: запросы идут поштучно. Batch есть у самих провайдеров (у Z.ai — проверить), но классический batch — это асинхронная обработка с задержкой от минут до 24 часов и скидкой. Для интерактивного цикла «Claude ждёт ответ инструмента» это не подходит.
- Что реально даёт выигрыш при многих одновременных изменениях: **параллельные запросы** (валидатор не ограничен слотами локальной модели) и **кэш промпта** (общая часть: системный промпт, `style.md`, контекст сервиса — идёт первой, чтобы срабатывал cache read).
- Где batch может пригодиться: фоновые операции без ожидания Claude, например массовое ревью при `foreman-code-ops review --all` или ночная проверка качества. Это этап 7.

**Замер на этапе 4:** латентность (p50/p95) одиночного ревью через OpenRouter для flash и flashx, при 1/4/8 параллельных запросах; доля cache read; стоимость на 100 ревью. Результаты — в `docs/providers.md`.

---

## 8. Agent SDK

Agent SDK существует для TypeScript и Python, не для Go. Поэтому:
- `sidecar/agent-sdk` — маленький Node-процесс со stdin/stdout JSON-RPC: принимает `{system, prompt, schema}` и возвращает ответ.
- foreman-code-ops запускает его лениво при `provider: agent-sdk` и держит живым.
- Sidecar не обязателен: если пользователь им не пользуется, Node не нужен.
- Это последний по приоритету провайдер (этап 7). Для v1 достаточно `openai`, `openrouter`, `anthropic` и `none`.

---

## 9. Граф кода: `codebase-memory-mcp` или `graphify`

| | codebase-memory-mcp | graphify |
|---|---|---|
| Форма | MCP-сервер, один статический бинарник | skill + CLI (Python, `uv tool install graphifyy`) |
| Как Claude читает граф | живые запросы через MCP-инструменты | файлы `graphify-out/`: `graph.json`, `GRAPH_REPORT.md`, `graph.html`; CLI `graphify explain`, `graphify path` |
| Обновление | `auto_index` или вызов через CLI | git-хук (`graphify hook install`) или повторный запуск |
| Сильные стороны | быстрые точечные запросы во время работы | кроме кода разбирает доки, PDF, изображения; рёбра помечены как извлечённые или выведенные; понятный отчёт |
| Для нашего цикла правок | лучше: частые мелкие запросы после каждого коммита | хорошо для обзора проекта и связей; git-хук сам ложится на наш автокоммит |

**Решение:** не выбирать один вариант жёстко, а поддержать оба через `GraphProvider`:
- `codebase-memory` — по умолчанию для цикла правок;
- `graphify` — альтернатива. После нашего коммита его обновляет git-хук, `References` берётся из `graph.json` или через `graphify path`/`explain`;
- `noop` — без графа.

Обнаружение связей между сервисами (§11a) делаем сами, но результат можно отдавать в любой из графов как дополнительные рёбра.

> Точные CLI-команды и формат `graph.json` уточнить на этапе 5 и вынести в конфиг провайдера.

**Интеграция**
```go
type GraphProvider interface {
    Reindex(files []string) error
    References(file, symbol string) ([]Ref, error)   // для delete_symbol
    Available() bool
}
```
- `codebase-memory`: после коммита вызывается переиндексация. Если включён `auto_index`, этот шаг пропускается, остаётся только проверка готовности. `References` идёт через CLI-запрос к графу.
- `noop`: граф не настроен. `delete_symbol` тогда ищет ссылки grep-ом по имени и предупреждает о неточности.
- Claude читает граф напрямую через MCP самого `codebase-memory-mcp`. Наш сервер использует граф только для переиндексации и проверки ссылок.
- Свежая сигнатура в ответе `upsert_symbol` гарантирует, что Claude видит актуальное состояние, даже если индекс ещё обновляется.

> Точные имена инструментов и CLI-аргументов `codebase-memory-mcp` уточнить по README на этапе 5 и вынести в конфиг (`graph.cli.reindex`, `graph.cli.references`), чтобы поддерживать аналоги без изменения кода.

---

## 10. Git

- Перед операцией: проверить, что затронутые файлы не имеют незакоммиченных изменений пользователя. Если имеют, есть два режима (конфиг `git.dirty`): `refuse` (по умолчанию, вернуть ошибку Claude) или `include`.
- Коммитятся только файлы, затронутые операцией (`git add <files>`), никогда `-A`.
- Формат сообщения:
  ```
  foreman: upsert service.BookService.List

  spec: return paginated list of books via repository
  Foreman-Written-By: local (qwythos-9b-q4_k_m)
  Foreman-Reviewed-By: openrouter/z-ai/glm-5.3-flash
  Foreman-Attempts: build=1 review=1
  ```
- Коммиты идут в текущую ветку. Отдельная ветка (`git.branch: foreman/<session>`) включается только опцией.
- В монорепо операция коммитит файлы одного или нескольких сервисов одним коммитом. В сообщении указываются затронутые сервисы (`foreman(backend,frontend): ...`).
- Если коммит упал (хуки pre-commit и т. п.), файл не откатывается. Ошибка возвращается Claude вместе с выводом хука.
- Трейлеры позволяют собирать метрики прямо из `git log`.

---

## 11. Стиль проекта (`.foreman/style.md`)

- Создаёт Claude через `style_set` после анализа кодовой базы, например по первой просьбе пользователя или по skill-инструкции «если файла нет, предложи создать».
- Рекомендуемые секции (шаблон в skill): ошибки, логирование, DI/конструкторы, именование слоёв, контекст/отмена, комментарии, специфика по языкам (`## Go`, `## TypeScript`, …).
- В промпт генератора попадает общая часть и секция языка текущего файла. Это экономит контекст слота.
- Файл коммитится как обычное изменение и версионируется вместе с проектом.

---

## 11a. Монорепо и связи между сервисами

### `.foreman/workspace.yaml`
```yaml
services:
  - name: backend
    root: backend            # где go.mod
    lang: go
    exposes:
      openapi: backend/api/openapi.yaml   # опционально
  - name: frontend
    root: frontend           # где package.json / tsconfig.json
    lang: typescript
    consumes:
      - service: backend
        base_url_env: VITE_API_URL
  - name: worker
    root: services/worker
    lang: python
```
- Если файла нет, `foreman-code-ops init` предлагает черновик: ищет маркеры корней (`go.mod`, `package.json`, `pyproject.toml`, `tsconfig.json`) и записывает их. Claude может поправить результат через инструмент `workspace_set`.
- Каждый файл относится к ближайшему сервису по `root`. Гейт, форматтеры и тесты запускаются в корне этого сервиса, а не проекта.
- Операция может затронуть несколько сервисов: гейт запускается в каждом из них.

### Обнаружение связей
Команда и инструмент `discover_links` строят карту «кто кого вызывает» между сервисами. Детекторы — плагины:

| Детектор | Что ищет |
|---|---|
| HTTP-роуты (сервер) | Go: `net/http`, chi, gin, echo, fiber; Python: FastAPI, Flask, Django; TS: express, fastify, hono, NestJS |
| HTTP-вызовы (клиент) | `fetch`, axios, ky, сгенерированные клиенты; Go `http.Client`; Python `httpx`/`requests` |
| Контракты | OpenAPI/Swagger, `.proto` (gRPC), GraphQL-схемы |
| Сообщения | топики Kafka/NATS/RabbitMQ/Redis pub-sub по строковым литералам и конфигам |
| Инфраструктура | `docker-compose.yml`, k8s-манифесты, `.env*`: имена сервисов, порты, URL |

**Сопоставление:** клиентский вызов `GET /api/books` (с учётом base URL из env или compose) сопоставляется с серверным роутом `GET /books` в `backend` с учётом префиксов роутера. Каждая связь получает уровень уверенности: `contract` (подтверждена OpenAPI/proto), `matched` (совпали метод и путь) или `guessed` (совпал только путь, или путь собран динамически).

**Результат:** `.foreman/links.json` (коммитится) и инструмент `get_links {service?, symbol?}`, который возвращает, например, `frontend.useBooks → GET /books → backend.BookHandler.List`.

**Как это используется**
- Claude видит, что изменение хендлера в бэкенде затрагивает конкретные вызовы на фронте.
- `upsert_symbol` принимает в `context` ссылку на символ другого сервиса. Например, TS-хук получает Go-хендлер и DTO, и локальная модель видит реальную форму ответа.
- `delete_symbol` предупреждает, если удаляемый хендлер вызывается из другого сервиса.
- После коммита, если изменился роут или клиентский вызов, связи затронутых файлов пересчитываются инкрементально.

---

## 12. Адаптеры

### Интерфейс
```go
type Adapter interface {
    Lang() string
    Extensions() []string
    Find(src []byte, symbol string) (*Node, error)
    Outline(src []byte) []Signature                  // сигнатуры без тел для контекста
    InsertPoint(src []byte, anchor string) (int, error)
    NewFile(path string, opts NewFileOpts) []byte
    InsertImports(src []byte, imports []string) ([]byte, error)
    FixImports(ws Workspace, file string) error
    Format(ws Workspace, file string) error
    Check(ws Workspace, file string) ([]Diagnostic, error)
    Test(ws Workspace, target, filter string) (*TestResult, error)
    PromptFragment() string
}
```
Generic-адаптер реализует этот интерфейс по YAML. Встроенные языки описаны тем же YAML, и это главный пример для контрибьюторов.

### Языки v1

| Язык | Грамматика | Символы | format | imports | check | test (по запросу) |
|---|---|---|---|---|---|---|
| Go | go | func, method (`Recv.Name`), type, interface | gofmt | вставка + goimports | `go build`, `go vet` для пакета | `go test ./pkg -run` |
| Python | python | def, async def, class, `Class.method` | ruff format | вставка + `ruff check --fix --select I,F401` | `ruff check` + pyright (если есть) | pytest `-k` |
| TS/JS | typescript / javascript | function, arrow-const, class, `Class.method`, interface, type | prettier или biome (по наличию конфига) | вставка + organize imports (biome/eslint) | `tsc --noEmit --incremental` (для JS — только если есть `checkJs`), eslint/biome | vitest/jest (по `package.json`) |
| TSX/JSX | tsx | всё из TS + компоненты (`function X()` / `const X = () =>` с JSX) | то же | то же | то же | то же |

**Особенности**
- **Python:** гейт слабый (нет компиляции). Рекомендуется включённый валидатор, а pyright настоятельно советуется в документации.
- **TS:** `tsc` запускается по проекту, поэтому нужен инкрементальный режим и тёплый кэш (`tsBuildInfoFile` в `.foreman/cache`). В v2 — tsserver/LSP.
- **TSX:** символ компонента = имя экспортируемой функции или константы. Хуки (`useX`) обрабатываются как обычные функции.
- **Инструменты проекта** определяются автоматически: biome или prettier и eslint, pyright или mypy, npm/pnpm/yarn/bun по lock-файлу. Можно переопределить в `.foreman/config.yaml`.

---

## 13. JSON-схемы

**Генератор**
```json
{
  "type": "object",
  "required": ["code"],
  "properties": {
    "code":    { "type": "string" },
    "imports": { "type": "array", "items": { "type": "string" } }
  }
}
```
`imports` — строки в синтаксисе языка: `"context"`, `"from app.repo import BookRepo"`, `"import { BookService } from '../service/book'"`.

**Валидатор**
```json
{
  "type": "object",
  "required": ["ok", "severity", "issues"],
  "properties": {
    "ok":       { "type": "boolean" },
    "severity": { "enum": ["none", "minor", "major"] },
    "issues": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["problem"],
        "properties": {
          "line":    { "type": "integer" },
          "problem": { "type": "string" },
          "fix":     { "type": "string" }
        }
      }
    },
    "rewrite": {
      "type": ["object", "null"],
      "properties": {
        "code":    { "type": "string" },
        "imports": { "type": "array", "items": { "type": "string" } }
      }
    }
  }
}
```

---

## 14. Интеграция с Claude Code (плагин)

- **`.mcp.json`**: запуск `foreman-code-ops --project ${CLAUDE_PROJECT_DIR}`. Рядом в README — инструкция подключить `codebase-memory-mcp`.
- **Skill `foreman-code-ops`** (на английском):
  - **все поля, которые Claude передаёт в MCP (`spec`, `interface`, `commit_message`, `style.md`), пишутся на английском**, даже если пользователь общается на русском. Так лучше для локальной модели и для истории git в open-source проектах. С пользователем Claude продолжает говорить на его языке;
  - перед правкой смотреть граф и передавать `context` ссылками, а не текстом;
  - порядок по слоям (данные → сервис → транспорт), дожидаясь `signature` предыдущего шага;
  - когда использовать `mode=direct` (сложная логика, алгоритмы);
  - когда просить `run_tests`;
  - что делать при `failed` (прочитать диагностики, уточнить ТЗ или написать самому через `mode=direct`);
  - при отсутствии `.foreman/style.md` предложить пользователю создать его.
- **Запрет обхода**: шаблон `.claude/settings.json` с `permissions.deny` для `Edit`/`Write` по `**/*.go`, `**/*.py`, `**/*.{ts,tsx,js,jsx,mjs,cjs}`. Команда `foreman-code-ops init` кладёт его в проект.
- **Hooks**: опциональный `PostToolUse` для уведомлений; переиндексацию графа делает сам сервер (§9).

---

## 15. Этапы

### Этап 0 — каркас и open-source гигиена (2 дня)
- [x] Go-модуль, MCP-сервер, заглушки инструментов
- [x] Go-модуль `github.com/MarshMeg/foreman-code-ops`
- [x] Загрузка конфига: CLI → env → проект → глобальный, включая блок `limits`
- [x] Journal, логирование
- [x] LICENSE (MIT), README (en) + README.ru.md, CONTRIBUTING, CI (lint + test), workflow релизов
- [x] Промпты для LLM и описания инструментов MCP — на английском

### Этап 1 — правка файлов без LLM, generic-адаптер (3–4 дня)
- [x] `workspace.yaml`: корни сервисов, привязка файла к сервису, автообнаружение маркеров в `init`
- [x] tree-sitter, generic YAML-адаптер
- [x] Адаптер Go на YAML: `Symbols` (outline), `Find`, `NewFile`; вставка после якоря — `Find` + `edit.InsertAfter` вместо `InsertPoint`
- [x] `edit`: замена, вставка, удаление по диапазонам
- [x] `workspace`: diff, атомарная запись, откат
- [x] temp-копии для прогона гейта — заменены на overlay (этап 2); для языков без overlay — этап 6
- [x] `gitops`: проверка dirty, stage файлов, коммит с трейлерами
- [x] Инструменты `get_symbol`, `delete_symbol` (grep-проверка), `create_file`, `delete_file`
- [x] Тесты на `testdata/go-layered`

### Этап 2 — гейт Go (1–2 дня)
- [x] `InsertImports`, `FixImports`, `Format`, `Check`, `Test` — описаны в `toolchain` адаптера (YAML), исполняет `internal/gate`
- [x] Единый формат диагностик
- [x] Инструменты `check`, `run_tests`
- [x] Кандидаты проверяются без записи на диск: `-overlay` для `go build`/`vet`/`test` (вместо temp-копий)
- [x] `go build` по всему модулю (`scope: all`), чтобы ловить сломанные вызовы; `go vet` — по затронутым пакетам
- [x] `delete_symbol`/`delete_file` проходят гейт: goimports чистит импорты, поломка сборки → отказ (или коммит с диагностиками при `force`)

### Этап 3 — генератор, слоты, бюджет (3 дня)
- [x] Провайдер `openai` с `response_format` и запасным GBNF (поле `json_schema` llama.cpp, сервер сам строит грамматику; автопереключение при HTTP 400)
- [x] Слоты из конфига, сверка с llama-server (`/props`), предупреждение при расхождении, `scheduler`
- [x] `budget.fit` с `/tokenize` и приоритетами обрезки
- [x] Промпты `generate.md`, `fix.md`; фрагмент `prompt.md` в адаптере
- [x] `upsert_symbol` с циклом build-fix
- [x] Генерация без глобальной блокировки; при изменении файла во время генерации код накладывается заново и перепроверяется
- [x] Ответ должен определять ровно запрошенный символ: лишние символы — ошибка для цикла исправлений

### Этап 4 — валидатор и фолбэк (2–3 дня)
- [x] Провайдеры `openrouter`, `anthropic` (официальный Go SDK, structured outputs вместо forced tool use — его отклоняют текущие модели), `none`
- [x] Серверная проверка JSON Schema + повтор
- [ ] Замер скорости и цены `glm-5.3-flash` / `flashx`, параллельность, кэш промпта (§7a) — команда `foreman-code-ops bench-review` готова, прогон нужен с ключом OpenRouter
- [x] `review.md`, `fallback.md`, полный pipeline, `mode=direct`
- [x] Фолбэк сначала пробует `rewrite` из последнего ревью; недоступный ревьюер не роняет прошедший гейт результат (коммит без `Reviewed-By` + предупреждение)

### Этап 5 — стиль, граф, плагин (2–3 дня)
- [x] `style_get` / `style_set`, подмешивание секций по языку
- [x] `GraphProvider`: codebase-memory (CLI `cli <tool>`, JSON на stdin), graphify (`graph.json`), `command` (шаблоны argv в конфиге), noop
- [x] `delete_symbol` через `References`, с откатом на поиск по имени и предупреждением
- [x] Плагин: `plugin.json`, `.mcp.json`, skill, `marketplace.json`; `foreman-code-ops init` с permissions (`Edit(**/*.go)` покрывает Write/MultiEdit), `.gitignore`, `--mcp`
- [x] E2E: эндпоинт поиска книг в Go-проекте с тремя слоями (через MCP, генератор + валидатор)
- [ ] Хук `PostToolUse` для уведомлений — не сделан: переиндексацию делает сервер, полезной нагрузки у хука пока нет

> Имена инструментов плагина: `mcp__plugin_foreman-code-ops_foreman__<tool>` (а не `mcp__foreman__…`, как в §0). Плагин не может сам добавить `permissions.deny` — это делает только `init`.

### Этап 6 — Python, TS/JS, TSX (5–7 дней)
- [x] Адаптер Python (ruff, pyright или mypy, pytest)
- [x] Адаптер TS/JS (tsc incremental, biome/prettier/eslint, vitest/jest)
- [x] Адаптер TSX/JSX (компоненты по заглавной букве, хуки как функции)
- [x] Автоопределение инструментов: условия `when` (конфиги, зависимости package.json, `checkJs`), группы «первый подходящий», `node_modules/.bin` и `.venv/bin`
- [x] Фикстуры (`py-layered`, `ts-layered`, `tsx-components`) и E2E на каждый язык
- [x] Проверка кандидатов без записи на диск для языков без overlay: синхронизируемая копия корня сервиса вне проекта
- [x] `init` запрещает Edit только для языков проекта; `.mjs`/`.cjs`-конфиги остаются редактируемыми

### Этап 6a — связи между сервисами (4–5 дней)
- [x] Детекторы HTTP-роутов и вызовов для Go, Python, TS (с префиксами роутеров, в том числе между файлами)
- [x] OpenAPI/proto как источник `contract`-связей
- [x] docker-compose/.env для base URL (хост → сервис, путь base URL → префикс вызова)
- [x] `discover_links`, `get_links`, `.foreman/links.json`; пересчёт при каждом коммите foreman — в том же коммите (полное пересканирование, а не инкремент: регулярки дешёвые)
- [x] Межсервисный `context` в `upsert_symbol`, отказ в `delete_symbol` для обработчиков с вызовами из других сервисов
- [x] E2E: `testdata/monorepo` (Go-бэкенд + TSX-фронт + Python-воркер): хуки `useBooks`/`useBook`, новый роут
- [ ] Детектор сообщений (Kafka/NATS/RabbitMQ/Redis) из таблицы §11a — не сделан

### Этап 7 — Agent SDK и улучшения
- [ ] Sidecar `agent-sdk` и провайдер
- [ ] LSP-диагностики (gopls, pyright, tsserver) вместо CLI
- [ ] `foreman-code-ops stats`: отчёт по journal и git-трейлерам
- [ ] Документация по написанию адаптеров, примеры для Rust/C#

### Этап 8 — релиз
- [ ] Бинарники для Linux (amd64/arm64) нативной сборкой на раннерах; macOS — сборка в CI без гарантий; Windows — не в v1
- [ ] Публикация плагина (marketplace-репозиторий)
- [ ] Демо-видео и пример-проект

---

## 16. Тестирование

- **Unit:** адаптеры на фикстурах (методы, вложенные классы, arrow-функции, компоненты, комментарии и декораторы сохраняются).
- **Budget:** обрезка контекста при разных размерах слота.
- **Pipeline:** мок-провайдеры со сценариями (битый код → фикс; review major ×3 → фолбэк; валидатор `none` + провал гейта → `failed`).
- **Git:** dirty-дерево, pre-commit хук падает, работа в отдельной ветке.
- **E2E:** по проекту на язык из `testdata/`.
- **Качество:** 20–30 типовых задач на язык; метрика — доля `written_by=local` без фолбэка. Прогон в CI по расписанию с локальной моделью не нужен, только на машине автора.

---

## 17. Риски

| Риск | Митигация |
|---|---|
| 9B ломает формат ответа | `json_schema`/GBNF, серверная проверка схемы, повтор |
| Контекст не влезает в слот | приоритеты обрезки, `Outline` вместо полных файлов, ошибка `context_too_large` |
| Цикл на стилистике | `minor` не запускает новый круг |
| Слабый гейт Python/JS | рекомендовать валидатор и pyright/`checkJs`, предупреждение в `status` |
| Медленный `tsc` | incremental + кэш; v2 — LSP |
| Без валидатора проходит плохой код | Claude видит `written_by` и отсутствие review; skill велит просматривать diff |
| Коммит смешивает правки пользователя | проверка dirty, stage только затронутых файлов |
| Изменения API `codebase-memory-mcp` | команды графа в конфиге, `noop`-фолбэк |
| Claude обходит MCP | `permissions.deny` через `foreman-code-ops init` |

---

## 18. Метрики успеха

- ≥ 60–70 % рутинных задач (CRUD, слои, маппинг, простые компоненты) закрываются `written_by=local`.
- Среднее число review-кругов ≤ 1.
- 0 повреждённых файлов и 0 коммитов с чужими изменениями.
- Для рутинных задач время на задачу не больше, чем если бы Opus писал сам.
