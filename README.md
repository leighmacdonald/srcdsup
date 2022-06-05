# stvup

Automatically upload new SourceTV recordings to a remote server via ssh/scp.

This tool is currently designed to only upload all but the most recent recording.

## Configuration

All configuration is currently handled via the following environment vars

- `STV_REMOTE_ROOT` Remote directory to send .dem files
- `STV_LOCAL_ROOT` Local directory where .dem files are stored
- `STV_PRIVATE_KEY` Path to local private key
- `STV_USERNAME` Remote SSH username
- `STV_HOST` Remote SSH Host
- `STV_PORT` Remote SSH Port, default: 22
- `STV_PASSWORD` If `STV_PRIVATE_KEY` is set, the private key passphrase, otherwise a standard ssh user password

## Docker

Example use via `docker run`.

     docker run \
        -v "/home/leigh/projects/stvup/stv_id_rsa:/app/id_rsa.key" \
        -v "/demos:/demos" \
        -e STV_PRIVATE_KEY="/app/id_rsa.key" \
        -e STV_USERNAME="tf2server" \
        -e STV_PASSWORD="" \
        -e STV_REMOTE_ROOT="./stv_demos/example-server-1" \
        -e STV_LOCAL_ROOT="/demos" \
        -e STV_HOST="example-server-1.host.com" \
        -e STV_PORT="22" \
        --rm -it leighmacdonald/stvup:master
        
    
    