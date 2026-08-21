#!/bin/sh
exec ssh -i "$GIT_PROVIDER_SSH_KEY" -o IdentitiesOnly=yes "$@"
