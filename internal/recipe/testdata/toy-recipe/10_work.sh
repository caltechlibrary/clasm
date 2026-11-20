#!/bin/bash
# step: work
# changes: nothing
if [ ! -e /var/tmp/clasm-toy-ok ]; then echo "forced failure: flag missing"; exit 75; fi
echo "COUNT work=1"
