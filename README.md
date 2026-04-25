# squirrel

Database-aware backup for PostgreSQL and MySQL/MariaDB. Content-addressed, AES-256-GCM encrypted, and deduplicated — with first-class support for WAL streaming, binlog capture, and physical base backups.

## Features

- **Content-addressed storage** — SHA-256 blob IDs, automatic deduplication across hosts and time
- **AES-256-GCM encryption** — random master key wrapped with Argon2id KDF; change passwords without re-encryption
- **Rabin CDC chunking** — variable-size chunks (512 KiB–8 MiB), excellent deduplication for similar datasets
- **zstd compression** — level 3, per blob, before encryption
- **Multiple backends** — local filesystem, S3/MinIO, Azure Blob, Google Cloud Storage, SFTP
- **PostgreSQL** — physical base backup + continuous WAL streaming; PITR with `recovery.signal`
- **MySQL/MariaDB** — logical SQL dump + binlog streaming (position-based and GTID); physical data-dir backup
- **Retention policies** — keep-last, keep-daily, keep-weekly, keep-monthly, keep-yearly
- **Parallel uploads** — 4-worker blob upload pool
- **FUSE mount** — read-only filesystem from any snapshot (Linux, macOS, Windows with WinFsp)
- **Config system** — YAML profiles with secret providers (env, file, keyring, Vault, SOPS, age, 1Password)
- **Daemon + scheduler** — built-in cron scheduler with Prometheus metrics

## Installation

### Binary releases

Download from [GitHub Releases](https://github.com/elpol4k0/squirrel/releases) for Linux, macOS, and Windows (amd64/arm64).

### Homebrew (macOS/Linux)

```bash
brew install elpol4k0/tap/squirrel
```

### Go

```bash
go install github.com/elpol4k0/squirrel/cmd/squirrel@latest
```

### Windows – Build from source

The `mount` command requires [WinFsp](https://winfsp.dev/) (Windows FUSE).
Without it, build with CGO disabled — all other commands work fully:

```powershell
$env:CGO_ENABLED = "0"
go build -o squirrel.exe .\cmd\squirrel\
```

Install WinFsp from [winfsp.dev](https://winfsp.dev/) if you need snapshot mounting on Windows.
After installation, rebuild without the flag to enable `mount`.

### Docker

```bash
docker pull ghcr.io/elpol4k0/squirrel:latest
```

## Quickstart

### File backup

```bash
# Initialize a repository
squirrel init --repo /backup/myrepo

# Back up a directory
squirrel backup --path /home/user/data --repo /backup/myrepo --tag daily

# List snapshots
squirrel snapshots --repo /backup/myrepo

# Restore a snapshot
squirrel restore abc12345 --repo /backup/myrepo --target /restore/data

# Apply retention policy
squirrel forget --repo /backup/myrepo --keep-last 7 --keep-daily 30 --prune
```

### PostgreSQL backup

```bash
# One-shot: base backup + WAL streaming (stops on Ctrl-C)
squirrel backup postgres \
  --dsn "postgres://replicator:pw@localhost/postgres" \
  --repo s3:mybucket/pg-backup \
  --tag production

# WAL-only (after a base backup exists)
squirrel backup postgres \
  --dsn "postgres://replicator:pw@localhost/postgres" \
  --repo s3:mybucket/pg-backup \
  --wal-only

# Restore with PITR
squirrel restore postgres abc12345 \
  --repo s3:mybucket/pg-backup \
  --target /var/lib/postgresql/17/main \
  --pitr "2026-04-20 14:30:00" \
  --verify
```

### MySQL/MariaDB backup

```bash
# Logical dump + binlog streaming
squirrel backup mysql \
  --dsn "root:pw@tcp(localhost:3306)/" \
  --repo /backup/mysql \
  --tag daily

# Restore dump and extract binlogs for replay
squirrel restore mysql abc12345 \
  --repo /backup/mysql \
  --dsn "root:pw@tcp(localhost:3306)/" \
  --binlog-dir /tmp/binlogs

# Physical restore with InnoDB recovery level
squirrel restore mysql abc12345 \
  --repo /backup/mysql \
  --target /var/lib/mysql \
  --innodb-recovery 1
```

### S3 backend

```bash
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...

squirrel init --repo s3:mybucket/prefix
squirrel backup --path /data --repo s3:mybucket/prefix
```

### SFTP backend

```bash
squirrel init --repo sftp://user:pass@backup-host.example.com/backups/myrepo
squirrel backup --path /data --repo sftp://user:pass@backup-host.example.com/backups/myrepo
```

## Configuration

Create `~/.config/squirrel/config.yml`:

```yaml
repositories:
  production:
    url: s3:mybucket/prod
    password: ${env:SQUIRREL_PASSWORD}

  offsite:
    url: sftp://backup@offsite.example.com/backups
    password-file: /etc/squirrel/offsite.key

profiles:
  pg-daily:
    repository: production
    source:
      type: postgres
      dsn: ${vault:secret/pg#replication_url}
    retention:
      keep-daily: 7
      keep-weekly: 4
      keep-monthly: 12
    prune: true
    tags: [postgres, daily]
    hooks:
      post-success:
        - webhook: https://monitoring.example.com/healthcheck/pg-daily
```

Run a profile:

```bash
squirrel run pg-daily
```

Install as a scheduled job:

```bash
# systemd timer (Linux)
squirrel schedule install pg-daily --cron "0 2 * * *"

# launchd (macOS)
squirrel schedule install pg-daily --cron "0 2 * * *"

# Windows Task Scheduler
squirrel schedule install pg-daily --cron "0 2 * * *"
```

Run as a daemon with built-in scheduler:

```bash
squirrel daemon --metrics :9090
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         squirrel CLI                            │
│  backup │ restore │ snapshots │ forget │ prune │ check │ run … │
└──────────────────────┬──────────────────────────────────────────┘
                       │
              ┌────────▼────────┐
              │    repo layer   │  snapshots, trees, blob index
              │                 │  dedup, flush, GC / prune
              └────────┬────────┘
                       │
         ┌─────────────▼──────────────┐
         │     packer / crypto        │  Rabin CDC → zstd → AES-GCM
         └─────────────┬──────────────┘
                       │
     ┌─────────────────┼─────────────────────┐
     │                 │                     │
 ┌───▼───┐        ┌────▼────┐          ┌─────▼────┐
 │ local │        │   S3    │          │  Azure   │
 │  fs   │        │  MinIO  │          │  GCS     │
 └───────┘        └─────────┘          │  SFTP    │
                                       └──────────┘
```

### Pack format

Blobs are grouped into pack files:

```
[blob_0][blob_1]…[blob_N][encrypted_header][header_len: 4 bytes LE]
```

Each region is independently encrypted:

```
nonce(12) || ciphertext || GCM-tag(16)
```

The header maps blob IDs → offsets inside the pack. The index (stored separately) maps blob IDs → pack file + offset for fast lookup.

### Deduplication

All blobs are identified by SHA-256 of their plaintext. Before writing, squirrel checks the in-session `pending` map and the persistent index. Identical content across snapshots, hosts, and databases is stored only once.

### Key management

A random 256-bit master key is generated at `squirrel init`. It is wrapped with Argon2id (time=3, mem=64 MiB, threads=4) and stored encrypted in `keys/<id>`. Multiple keys are supported (`squirrel key add/remove/list`), all wrapping the same master key.

## Commands

| Command | Description |
|---|---|
| `squirrel init` | Initialize a new repository |
| `squirrel backup` | Back up files or a database |
| `squirrel backup postgres` | PostgreSQL base backup + WAL |
| `squirrel backup mysql` | MySQL/MariaDB logical or physical backup |
| `squirrel restore <id>` | Restore files from a snapshot |
| `squirrel restore postgres <id>` | Restore PostgreSQL base backup + WAL |
| `squirrel restore mysql <id>` | Restore MySQL dump or physical backup |
| `squirrel snapshots` | List snapshots |
| `squirrel forget` | Apply retention policy |
| `squirrel prune` | Remove unreferenced blobs |
| `squirrel check` | Verify repository integrity |
| `squirrel diff <a> <b>` | Show diff between two snapshots |
| `squirrel stats` | Repository statistics and dedup ratio |
| `squirrel mount <id> <mp>` | Mount snapshot as read-only filesystem (Linux/macOS; Windows requires [WinFsp](https://winfsp.dev/)) |
| `squirrel key add/remove/list` | Manage repository keys |
| `squirrel secrets set/list/delete` | Manage OS keyring secrets |
| `squirrel config init/validate/show` | Manage config file |
| `squirrel run <profile>` | Run a config profile |
| `squirrel daemon` | Run as a daemon with built-in scheduler |
| `squirrel schedule install/remove/list` | Manage OS-level schedules |
| `squirrel self-update` | Update to latest release |
| `squirrel pg drop-slot` | Drop a PostgreSQL replication slot |
| `squirrel completion` | Generate shell completions |

## Secret providers

Passwords and DSNs can be sourced from:

| Syntax | Source |
|---|---|
| `${env:VAR}` | Environment variable |
| `${file:/path}` | File contents |
| `${keyring:service/key}` | OS keyring (macOS Keychain, GNOME Keyring, Windows Credential Manager) |
| `${cmd:op read op://vault/item/field}` | Arbitrary command (1Password, pass, …) |
| `${vault:secret/myapp#field}` | HashiCorp Vault (uses `VAULT_ADDR` + `VAULT_TOKEN`) |
| `${sops:secrets.enc.yaml#db.password}` | Mozilla SOPS encrypted file |
| `${age:secrets.age}` | age-encrypted file |
| `${op://vault/item/field}` | 1Password native syntax (via `op` CLI) |

## PostgreSQL requirements

- PostgreSQL 12 or later
- A replication user: `GRANT REPLICATION ON DATABASE … TO replicator`
- `wal_level = replica` (or `logical`)
- Sufficient `max_wal_senders` and `max_replication_slots`

For PITR, WAL segments must be continuously streamed. Start streaming before the base backup, or use `--wal-only` after an existing base backup.

## MySQL/MariaDB requirements

- MySQL 5.7 / 8.x or MariaDB 10.5+
- Binary logging enabled: `log_bin = ON`
- A user with `RELOAD`, `REPLICATION SLAVE`, `REPLICATION CLIENT`, `SELECT` privileges
- For GTID streaming: `gtid_mode = ON`, `enforce_gtid_consistency = ON`

## License

MIT
