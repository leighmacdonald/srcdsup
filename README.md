# srcdsup

Automatically upload new logs & demo recordings from srcds to a remote server via ssh/https.

This tool is currently designed to only upload all but the most recent recordings. It does
not stream the current file as its being written.

## Configuration

All configuration is handled with a `srcdsup.yml` config file. Copy the example
and edit it.

    cp srcdsup_example.yml srcdsup.yml
    $EDITOR srcdsup.yml

[srcdsup_example.yml](srcdsup_example.yml) 

## Docker

Example use via `docker run`.

     docker run \
        -v "$(pwd)/stv_id_rsa:/app/id_rsa.key:ro" \
        -v "$(pwd)/srcdsup.yml:/app/srcdsup.yml:ro" \
        -v "/demos:/demos" \
        -v "/logs:/logs" \
        --rm -it ghcr.io/leighmacdonald/srcdsup:master
