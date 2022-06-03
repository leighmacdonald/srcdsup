# stvup

Automatically upload new SourceTV recordings to a remote server.

This tool is currently designed to only upload all but the most recent recording.

## Configuration

All configuration is currently handled via environment vars
- `STV_REMOTE_ROOT` Local directory where .dem files are stored
- `STV_LOCAL_ROOT` Remote directory to send .dem files
- `STV_PRIVATE_KEY` Path to local private key
- `STV_USERNAME` Remote SSH username
- `STV_HOST` Remote SSH Host
- `STV_PORT` Remote SSH Port, default: 22
- `STV_PASSWORD` If `STV_PRIVATE_KEY` is set, the private key passphrase, otherwise a standard ssh user password

