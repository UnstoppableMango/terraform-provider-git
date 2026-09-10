#!/bin/sh
case "$1" in
	Username*) echo "$GIT_PROVIDER_USERNAME" ;;
	*) echo "$GIT_PROVIDER_TOKEN" ;;
esac
