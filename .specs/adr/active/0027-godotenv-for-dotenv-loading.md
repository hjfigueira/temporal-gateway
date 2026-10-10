# ADR-027: Load `.env` with `joho/godotenv`, not a hand-rolled parser

**Status:** Accepted. Supersedes the `.env` parsing part of [ADR-011](0011-var-expansion-over-raw-config-bytes.md); its precedence rule stands.

**Related features:** [`environment-config.feature`](../../features/environment-config.feature)

## Context

`internal/dotenv` was a ~75-line line parser, and it got common `.env`
content wrong. `KEY=value # comment` kept the comment in the value.
`export KEY=value` produced a variable literally named `export KEY`.
Escapes and multi-line quoted values weren't supported. The dotenv format
has many such edge cases, and `github.com/joho/godotenv` is the de facto Go
implementation of it.

`${VAR}` expansion of `config.yml` / `api-spec.yaml` stays hand-rolled
(ADR-011). Every expansion library checked (`a8m/envsubst`,
`drone/envsubst`, stdlib `os.Expand`) also expands bare `$VAR`, which would
mangle `$` in spec `pattern` regexes and secrets.

## Decision

The `LoadDotEnv` module (`internal/app`) calls `godotenv.Load(--env)` before loading config, and ignores
only `fs.ErrNotExist`. The `internal/dotenv` package is deleted. godotenv
keeps ADR-011's rule that variables already set in the real environment
win over the file.

## Consequences

- Inline comments, `export`, escapes (`\n`) and multi-line quoted values
  now work.
- **`$` inside `.env` values:** in an unquoted or double-quoted value,
  `$NAME` / `${NAME}` (uppercase names) expands from variables defined
  earlier in the file or already in the environment, and an undefined
  one expands to an empty string, silently. `PASSWORD=Pa$SWORD1` loads as
  `Pa`. Write such values in single quotes (`'Pa$SWORD1'`) or escape the
  dollar (`Pa\$SWORD1`). Lowercase `$name` is left alone.
- A malformed line (e.g. no `=`) still fails startup, with godotenv's
  parse error.
- One more dependency. It's small, stable, and covered by `govulncheck` in
  CI (ADR-024).
