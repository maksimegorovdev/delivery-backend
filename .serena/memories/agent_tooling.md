# Работа агента с инструментами

- Serena: читать и править существующий Go-код через `lsp.get_symbols_overview`, `lsp.find_symbol`, `edit.*`; Bash — для `task`, `go test`, `git`. `Read` по коду для обзора и правки через `sed`/python не использовать. Serena видит файлы с build-тегом `integration`; gopls-диагностика Claude Code по ним жалуется «No packages found» — это шум, истина — `go vet -tags=integration` и `task lint`.
- Скиллы `golang-*` лежат в `.claude/skills/` и подключаются условно (`paths: **/*.go`): в списке `Skill` они появляются только после открытия `.go`-файла инструментом `Read`. Чтение через `cat` в Bash их не подключает, вызов даёт `Unknown skill`.
- Хуки предупреждают о подряд идущих чтениях без символьных инструментов: это сигнал перейти на Serena.
- Временные файлы и эксперименты — в scratchpad, не в проекте.
