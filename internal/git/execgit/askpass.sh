#!/bin/sh
case "$1" in
	Username*) echo "x-access-token" ;;
	*) echo "$GIT_PROVIDER_TOKEN" ;;
esac
