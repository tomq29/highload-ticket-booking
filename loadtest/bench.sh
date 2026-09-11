#!/usr/bin/env bash
# Runs both scenarios against every strategy and keeps the k6 summaries.
# The seat pool is rebuilt before each run so no scenario inherits the seats
# the previous one took.
set -euo pipefail

cd "$(dirname "$0")/.."

strategies=("${STRATEGIES:-pessimistic optimistic atomic}")
scenarios=("${SCENARIOS:-hot_seat spread}")
results=loadtest/results
mkdir -p "$results"

for strategy in ${strategies[*]}; do
    echo "==> strategy: $strategy"
    BOOKING_STRATEGY="$strategy" docker compose up -d --wait api

    for scenario in ${scenarios[*]}; do
        echo "==> $strategy / $scenario"
        docker compose exec -T db psql -q -U booking -d booking < loadtest/setup.sql
        docker compose run --rm k6 run "/scripts/${scenario}.js" \
            | tee "${results}/${strategy}-${scenario}.txt"
    done
done
