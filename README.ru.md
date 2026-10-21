# foreman-code-ops

[English version](README.md)

MCP-сервер и плагин для Claude Code, который делит работу над кодом между моделями:

- **Claude** планирует задачу и выбирает символы для изменения.
- **Локальная или дешёвая модель** (*генератор*) пишет код.
- **Гейт** (импорты, форматтер, линтер, сборка) и опциональный облачный **валидатор** проверяют результат.
- **Сервер** вставляет код через tree-sitter и коммитит каждое изменение.

Claude вызывает `upsert_symbol` вместо `Edit`/`Write`. Каждый успешный вызов — один коммит
с трейлерами `Foreman-Written-By`, `Foreman-Reviewed-By` и `Foreman-Attempts`.

> **Статус: ранняя разработка (этап 5).** Go поддерживается целиком: генерация под размер слота,
> гейт и цикл исправлений, ревью и фолбэк облачным валидатором, коммиты с трейлерами, стиль
> проекта, интеграция с графом кода и плагин для Claude Code. Дальше — адаптеры Python и TypeScript.
> План развития — в [документе проекта](docs/design/plan-v4.ru.md).

## Поддержка

| | v1 |
|---|---|
| Языки | Go, Python, TypeScript/JavaScript, TSX/JSX |
| Платформы | Linux; macOS — без гарантий; Windows — не в v1 |
| Генератор | любой OpenAI-совместимый эндпоинт (llama-server, vLLM, LM Studio), OpenRouter, Anthropic |
| Валидатор | OpenRouter (по умолчанию `z-ai/glm-5.3-flash`), OpenAI-совместимый, Anthropic или `none` |
| Граф кода | codebase-memory-mcp, graphify или без графа |

## Установка

1. Установите сервер (нужен C-компилятор для грамматик tree-sitter):

   ```bash
   go install github.com/MarshMeg/foreman-code-ops/cmd/foreman-code-ops@latest
   ```

2. Установите плагин для Claude Code (регистрация MCP-сервера и skill с правилами работы):

   ```
   /plugin install foreman-code-ops --marketplace MarshMeg/foreman-code-ops
   ```

3. Подготовьте проект:

   ```bash
   foreman-code-ops init --project .
   ```

   Команда создаёт черновик `.foreman/workspace.yaml`, добавляет в `.claude/settings.json`
   правила `permissions.deny` вида `Edit(**/*.go)`, чтобы правки исходников шли через foreman,
   и добавляет `.foreman/cache/` в `.gitignore`. Без плагина добавьте `--mcp`, чтобы прописать
   сервер в `.mcp.json`.

4. По желанию подключите граф кода ([docs/graph.md](docs/graph.md)), например codebase-memory-mcp.

С плагином инструменты называются `mcp__plugin_foreman-code-ops_foreman__<tool>`, при записи в
`.mcp.json` проекта — `mcp__foreman__<tool>`.

## Настройка

Настройки собираются по возрастанию приоритета: встроенные значения,
`~/.config/foreman-code-ops/config.yaml`, `<проект>/.foreman/config.yaml`, переменные `FOREMAN_*`,
флаги CLI. Пример — [config.example.yaml](config.example.yaml), описание — [docs/config.md](docs/config.md).

```bash
foreman-code-ops config --project .
```

выводит итоговую конфигурацию.

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
| `status` | провайдеры, слоты, очередь, граф, предупреждения |

Поля, которые Claude передаёт серверу (`spec`, `interface`, `commit_message`, `style.md`), пишутся
на английском, даже если вы общаетесь с Claude по-русски.

## Лицензия

[MIT](LICENSE)
