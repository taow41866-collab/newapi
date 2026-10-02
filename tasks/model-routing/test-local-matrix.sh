#!/bin/sh
# Run as root in the existing local WSL distribution; never targets production.
set -eu
cd /mnt/f/Projects/newapi/model
binary=../build/model-routing/model.test
"$binary" -test.run '^TestChannelProbeWeightSQLDialects/sqlite$' -test.v
TEST_MYSQL_DSN='root@unix(/var/run/mysqld/mysqld.sock)/codex_modelroute_20261002?parseTime=true' "$binary" -test.run '^TestChannelProbeWeightSQLDialects/mysql$' -test.v
runuser -u postgres -- env 'TEST_POSTGRES_DSN=host=/var/run/postgresql user=postgres dbname=codex_modelroute_20261002 sslmode=disable' "$binary" -test.run '^TestChannelProbeWeightSQLDialects/postgres$' -test.v
