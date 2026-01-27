#!/bin/bash

if [[ -n "$(git status --porcelain .)" ]]; then
    echo "Uncommitted generated files. Run 'make test' and commit results."
    echo "$(git status --porcelain .)"
    echo
    echo "$(git diff)"
    exit 1
fi
