# squirrel – TODO & Roadmap

## Phase 0 – Foundation
- [x] Go-Modul initialisiert (`github.com/elpol4k0/squirrel`)
- [x] Repository-Format (Packfiles + Header)
- [x] Content-addressed Storage (SHA-256 BlobIDs)
- [x] AES-256-GCM Verschlüsselung mit zufälligem Nonce
- [x] Argon2id KDF für Passwort-Wrapping
- [x] Zufälliger Master-Key (Passwort-Wechsel ohne Re-Encryption)
- [x] Rabin CDC-Chunker (Polynomial `0x3DA3358B4DC173`, 512KiB–8MiB)
- [x] zstd Level-3 Kompression (vor Encryption, per Blob)
- [x] Local-Filesystem-Backend (atomares Schreiben via `.tmp` → rename)
- [x] Index (in-memory `BlobID → PackBlobLocation`, encrypted JSON)
- [x] Within-Session-Dedup (`pending` map)
- [x] Snapshots (Tree-Blobs, JSON, verschlüsselt)
- [x] `squirrel init --repo <url>`
- [x] `squirrel backup --path --repo [--dry-run] [--tag]`
- [x] `squirrel snapshots --repo`
- [x] `squirrel restore <id> --repo --target`
- [x] `squirrel check --repo`
- [x] `squirrel forget --keep-last/daily/weekly/monthly/yearly [--prune]`
- [x] `squirrel prune --repo`
- [x] Retention-Policy (Bucket-Algorithmus)
- [x] GC / Prune (Mark-and-Sweep, Index-Rebuild)
- [x] Test-Suite (53 Tests: crypto, compress, chunker, backend, repo)

## Phase 1 – PostgreSQL
- [x] S3-Backend (`s3:bucket/prefix`, minio-go, Credentials aus ENV)
- [x] PostgreSQL-Adapter (`pglogrepl`)
- [x] `squirrel backup postgres --dsn --repo [--slot] [--wal-only]`
- [x] Base-Backup via `pg_basebackup`-Protokoll (TAR-Stream → Blobs → Tree)
- [x] WAL-Streaming (physische Replikation, 16 MiB Segmente, Keepalives)
- [x] WAL-Segmente mit korrekten PG-Dateinamen (`%08X%08X%08X`)
- [x] Replication-Slot-Verwaltung (`CreateSlot`)
- [x] Snapshot-Metadata (start_lsn, system_id, timeline)
- [x] `squirrel restore postgres <id> --repo --target [--pitr] [--pitr-lsn] [--wal-dir]`
- [x] TAR-Extraktion (reguläre Dateien, Verzeichnisse, Symlinks, Hardlinks)
- [x] WAL-Extraktion in Archiv-Verzeichnis
- [x] `recovery.signal` + `postgresql.auto.conf` schreiben
- [x] PITR-Zeitpunkt und LSN-Target

## Phase 2 – MySQL
- [x] MySQL-Adapter (DSN-Parser, `go-sql-driver/mysql`)
- [x] `squirrel backup mysql --dsn --repo [--database] [--binlog-only] [--physical --data-dir]`
- [x] Logisches Backup (pure-Go SQL-Dump, kein `mysqldump`-Binary)
  - `FLUSH TABLES WITH READ LOCK` + `START TRANSACTION` für konsistenten Snapshot
  - 200-Row INSERT-Batches, 4 MiB Chunks
  - `SHOW CREATE TABLE` für DDL
- [x] Physisches Backup (Lock → Data-Directory streamen → Unlock)
- [x] Binlog-Streaming (`go-mysql` BinlogSyncer, 64 MiB Segmente)
- [x] Binlog-Position in Snapshot-Metadata
- [x] `squirrel restore mysql <id> --repo --dsn [--binlog-dir] [--sql-only]`
- [x] SQL-Restore via `database/sql` (`multiStatements=true`)
- [x] Binlog-Extraktion für manuelle `mysqlbinlog`-Replay

## Phase 3 – Ops-Features & Config-System
- [x] YAML-Config mit `koanf/v2` (`~/.config/squirrel/config.yml`)
- [x] Repository-Definitionen (url, password, password-file, env)
- [x] Profil-Vererbung via `extends:` (transitiv, Zirkularität erkannt)
- [x] Retention-Policy pro Profil (`keep-daily/weekly/monthly/yearly/last`)
- [x] Secret-Provider: `${env:VAR}`
- [x] Secret-Provider: `${file:/path}`
- [x] Secret-Provider: `${keyring:service/key}` (OS-Keyring via `99designs/keyring`)
- [x] Secret-Provider: `${cmd:command args}` (beliebige CLI-Tools, 1Password, pass…)
- [x] Secret-Provider: `${vault:path#field}` (HashiCorp Vault, direkte HTTP-API)
- [x] Secret-Provider: `${sops:file.enc.yaml#dot.path}` (Mozilla SOPS via CLI)
- [x] `squirrel config init` – Skeleton-Config anlegen
- [x] `squirrel config validate` – Syntax + Referenzen prüfen
- [x] `squirrel config show <profile> [--reveal]` – effektive Config (Secrets maskiert)
- [x] `squirrel config migrate` – Config-Schema-Migration mit Backup
- [x] `squirrel run <profile> [--parallel N]`
- [x] Automatische Retention nach Backup (`prune: true` im Profil)
- [x] Hook-System: `pre-backup`, `post-success`, `post-failure`
  - `command: [...]` mit Env-Variable-Expansion
  - `webhook: <url>` mit 3× Retry + Backoff
- [x] `squirrel schedule install <profile>` → systemd-Timer (Linux)
- [x] `squirrel schedule install <profile>` → launchd-Plist (macOS)
- [x] `squirrel schedule install <profile>` → Windows Task Scheduler (`schtasks`)
- [x] `squirrel schedule remove/list`
- [x] `squirrel daemon [--profile] [--metrics :9090]`
  - `robfig/cron` built-in Scheduler
  - SIGHUP → Config-Reload ohne Neustart
- [x] Prometheus-Metriken (`/metrics`, Text-Format, ohne externe Library)
- [x] Progress-Bar (`schollz/progressbar`) – Wrapper für Streaming-Operationen
- [x] `squirrel snapshots --host X --type Y --tag Z` – Multi-Host-Filter

## Phase 4 – Weitere Backends & Advanced Features
- [x] Azure Blob Storage Backend (`az:container/prefix`, SharedKey + ConnectionString)
- [x] Google Cloud Storage Backend (`gs:bucket/prefix`, Application Default Credentials)
- [x] SFTP Backend (`sftp://user@host/path`, SSH-Agent + Key-Files)
- [x] `squirrel self-update [--check]` – GitHub Releases API
- [x] Config-Schema-Migration mit `.bak`-Backup

## Phase 5 – Advanced
- [x] `squirrel mount <id> <mountpoint>` – FUSE read-only Filesystem (Linux/macOS)
  - Stub mit klarer Fehlermeldung auf Windows
  - Direktes Blob-Laden per File-Read
- [x] `squirrel diff <snap-a> <snap-b>` – Added/Removed/Modified/Unchanged
- [x] Multi-Host-Deduplication (implizit durch content-addressed Storage + Host-Filter)

---

## Offen / Zukünftige Features

### Stabilität & Tests
- [ ] Integration-Tests mit testcontainers (PostgreSQL 14/15/16/17, MySQL 8)
- [ ] S3-Tests mit lokalem MinIO (testcontainers)
- [ ] SFTP-Tests mit lokalem OpenSSH-Container
- [ ] Fuzz-Tests für Packfile-Parser und Crypto
- [ ] End-to-End-Test: backup → prune → restore → verify

### Performance
- [x] Parallele Blob-Uploads (Worker-Pool im Packer, 4 concurrent uploads)
- [ ] Parallele Chunk-Verarbeitung beim Backup
- [ ] Streaming-Index (Memory-mapped oder BoltDB/Pebble für TB-Repos)
- [ ] Partial-Pack-Repack beim Prune (statt ganzen Pack löschen)

### PostgreSQL
- [ ] Incremental Base Backup (PG 15+ `pg_basebackup --incremental`)
- [ ] `squirrel restore postgres --pitr` mit automatischer WAL-Replay-Verification
- [ ] Replication-Slot-Cleanup-Command (`squirrel pg drop-slot --slot squirrel`)
- [ ] Support für mehrere Tablespaces mit korrekten Symlinks

### MySQL
- [ ] GTID-basiertes Binlog-Streaming (`StartSyncGTID`)
- [ ] MariaDB-Support (Flavor-Detection)
- [ ] Physisches Restore mit InnoDB-Recovery-Modus-Konfiguration

### Config & Security
- [x] `age`-Secret-Provider (`${age:encrypted_file}`)
- [x] 1Password Secret-Provider (`${op://vault/item/field}` via `op` CLI)
- [x] Multi-Key-Support pro Repository (wie restic – mehrere `keys/`)
- [x] `squirrel key add/remove/list` für Key-Verwaltung
- [x] `squirrel secrets set/list/delete` für OS-Keyring-Verwaltung

### CLI & UX
- [x] Progress-Bar in `backup files` und `restore` einbauen
- [x] `squirrel stats --repo` – Repository-Statistiken (Größe, Dedup-Ratio, Pack-Anzahl)
- [x] JSON-Output-Flag (`--json`) für `snapshots` und `stats`
- [ ] Shell-Completion (bash/zsh/fish) via `cobra` (bereits durch Cobra v1.2+ eingebaut)
- [x] `squirrel snapshots --latest` – nur neuestes Snapshot zeigen

### Distribution
- [x] `goreleaser` Konfiguration (Multi-Platform Binaries + Docker Images, `.goreleaser.yaml`)
- [x] GitHub Actions CI (Matrix: PG 14-17, MySQL 8, Linux/macOS/Windows, `.github/workflows/ci.yml`)
- [x] GitHub Actions Release (`.github/workflows/release.yml`)
- [x] Docker Image (`FROM alpine:3.21`, `Dockerfile`)
- [ ] Homebrew Formula
- [ ] README.md mit Quickstart, Architecture, Comparison-Table

### Phase 5 Rest
- [ ] `squirrel mount` auf Windows mit WinFsp (`cgofuse`)
- [x] `squirrel diff --stat` – nur Zahlen, keine Dateiliste
- [ ] Cross-Snapshot-Dedup-Report (`squirrel stats --dedup`)
- [ ] FUSE: Zeitstempel + Permissions korrekt aus Snapshot-Metadaten
