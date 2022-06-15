# srcdsup

Automatically upload new SourceTV recordings to a remote server via ssh/scp.

This tool is currently designed to only upload all but the most recent recording.

## Configuration

## Docker

Example use via `docker run`.

     docker run \
        -v "$(pwd)/stv_id_rsa:/app/id_rsa.key" \
        -v "$(pwd)/srcdsup.yml:/app/srcdsup.yml" \
        -v "/demos:/demos" \
        --rm -it ghcr.io/leighmacdonald/srcdsup:master
