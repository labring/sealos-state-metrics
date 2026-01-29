#!/bin/bash

HELM_OPTS=${HELM_OPTS:-""}

helm upgrade -i sealos-state-metrics \
    -n sealos --create-namespace \
    ./charts/sealos-state-metrics \
    ${HELM_OPTS}
