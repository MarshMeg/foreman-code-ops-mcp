# foreman-code-ops

[English version](README.md)

MCP-сервер и плагин для Claude Code, который делит работу над кодом между моделями:

- **Claude** планирует задачу и выбирает символы для изменения.
- **Локальная или дешёвая модель** (*генератор*) пишет код.
- **Гейт** (импорты, форматтер, линтер, сборка) и опциональный облачный **валидатор** проверяют результат.
- **Сервер** вставляет код через tree-sitter и коммитит каждое изменение.

Claude вызывает `upsert_symbol` вместо `Edit`/`Write`. Каждый успешный вызов — один коммит
с трейлерами `Foreman-Written-By`, `Foreman-Reviewed-By` и `Foreman-Attempts`.

> **Статус: ранняя разработка (этап 7).** Go, Python, TypeScript/JavaScript и TSX/JSX работают
> целиком: генерация под размер слота, гейт и цикл исправлений, ревью и фолбэк облачным
> валидатором, коммиты с трейлерами, стиль проекта, граф кода, плагин для Claude Code и связи
> между сервисами монорепо. Экспериментально: адаптеры Rust и C#, языковые серверы как быстрый
> этап гейта, провайдер на Agent SDK и `stats`.
> План развития — в [документе проекта](docs/design/plan-v4.ru.md).

## Поддержка

| | v1 |
|---|---|
| Языки | Go, Python, TypeScript/JavaScript, TSX/JSX; Rust и C# — экспериментально; свои адаптеры в проекте |
| Платформы | Linux; macOS — без гарантий; Windows — не в v1 |
| Генератор | любой OpenAI-совместимый эндпоинт (llama-server, vLLM, LM Studio), OpenRouter, Anthropic, Claude Agent SDK |
| Валидатор | OpenRouter (по умолчанию `z-ai/glm-5.3-flash`), OpenAI-совместимый, Anthropic, Claude Agent SDK или `none` |
| Граф кода | graphify (по умолчанию; через инструменты `graph_*` отвечает и на вопросы Claude), codebase-memory-mcp или без графа |

## Установка

foreman ставится один раз на машину и включается в каждом проекте отдельно. Плагин, бинарник,
graphify и настройки моделей — глобальные; MCP-сервер прописывается в `.mcp.json` проекта и
работает только в подключённых проектах.

### Один раз на машину

1. Установите плагин для Claude Code (скилы `setup`, `connect` и правила работы):

   ```
   /plugin marketplace add MarshMeg/foreman-code-ops-mcp
   /plugin install foreman-code-ops@foreman-code-ops
   ```

2. Выполните в Claude Code `/foreman-code-ops:setup`. Скил поставит бинарник и graphify, спросит,
   какие генератор и валидатор использовать, и запишет их в `~/.config/foreman-code-ops/config.yaml`.
   Вручную:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/MarshMeg/foreman-code-ops-mcp/main/scripts/install.sh | sh
   ```

   ```bash
   uv tool install graphifyy
   ```

   Скрипт установки выбирает архив релиза под вашу платформу (Linux amd64/arm64; macOS — без
   гарантий), сверяет его с `checksums.txt` и ставит в `~/.local/bin` (поменять можно через
   `FOREMAN_BIN_DIR` и `FOREMAN_VERSION`). Собрать из исходников (нужен C-компилятор для
   грамматик tree-sitter):
   `go install github.com/MarshMeg/foreman-code-ops-mcp/cmd/foreman-code-ops@latest`.
   Настройки моделей: [docs/config.md](docs/config.md).

### В каждом проекте

Выполните в Claude Code в проекте `/foreman-code-ops:connect`. Скил запустит
`foreman-code-ops init`, проверит результат, закоммитит его и продолжит работу в новой сессии,
где уже загружены инструменты foreman. Вручную:

```bash
foreman-code-ops init --project .
```

`init` записывает:

- `.foreman/workspace.yaml` — черновик списка сервисов (по `go.mod`, `package.json`,
  `tsconfig.json`, `pyproject.toml`, `Cargo.toml`, `*.csproj`); проверьте его;
- `.mcp.json` — сервер `foreman`, а в `.claude/settings.json` — `enabledMcpjsonServers`, чтобы он
  запускался без вопроса (`--mcp=false` пропускает этот шаг);
- `.claude/settings.json` — правила `permissions.deny` вида `Edit(**/*.go)`, чтобы правки
  исходников шли через foreman (`--no-permissions` их не добавляет);
- `CLAUDE.md` — раздел о том, что код Claude читает через граф foreman, а пишет через
  инструменты foreman (`--no-claude-md` его не пишет);
- `.gitignore` — `.foreman/cache/` и `graphify-out/`;
- `graphify-out/` — граф кода ([docs/graph.md](docs/graph.md)); с `--no-graph` foreman построит
  его при первом запросе к графу.

Затем откройте в проекте новую сессию Claude Code. Инструменты называются `mcp__foreman__<tool>`.

### Или пусть Claude всё сделает без плагина

Вставьте этот промт в Claude Code в своём проекте:

```text
Установи и подключи MCP-сервер foreman-code-ops (https://github.com/MarshMeg/foreman-code-ops-mcp)
к этому проекту. Сначала прочитай его README и docs/config.md.

1. Установи бинарник: выполни
   curl -fsSL https://raw.githubusercontent.com/MarshMeg/foreman-code-ops-mcp/main/scripts/install.sh | sh
   (если не получится — `go install github.com/MarshMeg/foreman-code-ops-mcp/cmd/foreman-code-ops@latest`),
   затем проверь `foreman-code-ops version` и что его каталог есть в PATH.
2. Установи graphify (граф кода): `uv tool install graphifyy` (или `pipx install graphifyy`).
   Не запускай `graphify install` и `graphify claude install`.
3. Установи плагин: `claude plugin marketplace add MarshMeg/foreman-code-ops-mcp`, затем
   `claude plugin install foreman-code-ops@foreman-code-ops`.
4. Спроси меня, какой генератор использовать (локальный llama-server: base URL, модель, число
   слотов и контекст на слот; или OpenRouter / Anthropic) и какой валидатор (по умолчанию
   OpenRouter с z-ai/glm-5.3-flash, Anthropic, Agent SDK или none). Запиши ответы в глобальный
   конфиг ~/.config/foreman-code-ops/config.yaml. Никогда не записывай API-ключи в файлы: скажи,
   какую переменную окружения мне задать.
5. Если генератор — llama-server, проверь, что он отвечает (GET <base_url>/models) и что его
   -np и -c совпадают со slots и slot_ctx.
6. Выполни `foreman-code-ops init --project .` и сверь .foreman/workspace.yaml с реальными
   сервисами репозитория (корни, языки, кто кого вызывает); исправь, если нужно.
7. Проверь, что инструменты гейта для языков проекта установлены (линтеры, проверка типов, тесты
   из таблицы языков в README), и перечисли, чего не хватает.
8. Закоммить .foreman/, .claude/settings.json, .mcp.json, CLAUDE.md и .gitignore отдельным
   коммитом и скажи мне открыть в проекте новую сессию Claude Code. В ней: вызови `status` и
   устрани предупреждения, вызови `graph_overview` и напиши короткий гайд по стилю через
   `style_set` (на английском, по секции на язык).
```

## Языки

| Язык | Файлы | Исправление | Проверка | Тесты |
|---|---|---|---|---|
| Go | .go | goimports | `go build` (модуль), `go vet` | `go test -run` |
| Python | .py .pyi | ruff (импорты, формат) | ruff, pyright или mypy | pytest `-k` |
| TypeScript | .ts .mts .cts | biome или prettier | `tsc` (проект), biome lint или eslint | vitest или jest `-t` |
| TSX/JSX | .tsx .jsx | то же | то же | то же |
| JavaScript | .js .mjs .cjs | то же | `tsc` при `checkJs`, biome lint или eslint | то же |
| Rust (экспериментально) | .rs | rustfmt | `cargo check` (крейт) | `cargo test` |
| C# (экспериментально) | .cs | — | `dotnet build` | `dotnet test --filter` |

Инструменты ищутся в `node_modules/.bin`, `.venv/bin` и PATH и выбираются по конфигам проекта. У
Python нет компилятора, поэтому поставьте pyright (или mypy) и не отключайте валидатор.

При `lsp.enabled: true` изменённые файлы сначала проверяют тёплые языковые серверы (gopls,
pyright, `tsc --lsp` или typescript-language-server): большинство ошибок находится быстрее
секунды. Команды из таблицы всё равно выполняются перед каждым коммитом. Проект может добавить
или переопределить адаптеры в `.foreman/adapters/` ([docs/adapters.md](docs/adapters.md)).

## Настройка

Настройки собираются по возрастанию приоритета: встроенные значения,
`~/.config/foreman-code-ops/config.yaml`, `<проект>/.foreman/config.yaml`, переменные `FOREMAN_*`,
флаги CLI. Пример — [config.example.yaml](config.example.yaml), описание — [docs/config.md](docs/config.md).

```bash
foreman-code-ops config --project .
```

выводит итоговую конфигурацию.

```bash
foreman-code-ops stats --project . --since 7d
```

показывает, что сделал foreman: исходы `upsert_symbol` (написано локально, валидатором, с первой
попытки), круги исправлений и ревью, задержки инструментов, токены и стоимость по моделям из
локального журнала, а также трейлеры `Foreman-*` из истории git (`--json` — для скриптов).

Локальный генератор с тремя слотами по 64k:

```bash
llama-server -m model.gguf -c 196608 -np 3 --port 8080
```

## Инструменты

| Инструмент | Назначение |
|---|---|
| `upsert_symbol` | создать или заменить функцию, метод, тип, класс, интерфейс или компонент |
| `get_symbol` | текст и сигнатура символа |
| `delete_symbol` | удалить символ (без `force` проверяет ссылки) |
| `create_file` / `delete_file` | явные файловые операции |
| `check` | прогон гейта без изменений |
| `run_tests` | тесты по запросу |
| `style_get` / `style_set` | чтение и изменение `.foreman/style.md` |
| `discover_links` / `get_links` | карта HTTP/gRPC-вызовов между сервисами ([docs/links.md](docs/links.md)) |
| `graph_overview` | структура проекта по графу кода: ключевые абстракции, сообщества |
| `graph_query` | символы, относящиеся к вопросу, с местоположением и связями |
| `graph_node` / `graph_path` / `graph_affected` | символ и его связи; путь между двумя; что от символа зависит |
| `status` | провайдеры, слоты, очередь, граф, предупреждения |

Поля, которые Claude передаёт серверу (`spec`, `interface`, `commit_message`, `style.md`), пишутся
на английском, даже если вы общаетесь с Claude по-русски.

## Лицензия

[MIT](LICENSE)
