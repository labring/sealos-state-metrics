#!/bin/bash

HELM_OPTS=${HELM_OPTS:-""}

helm upgrade -i state-metrics \
    -n sealos --create-namespace \
    ./charts/state-metrics \
    ${HELM_OPTS}
