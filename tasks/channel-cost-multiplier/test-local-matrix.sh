#!/bin/sh
# Disposable local WSL accounts; never accesses production.
set -eu
cd /mnt/f/Projects/newapi/controller
account="rvtest_$(date +%s)"
password=$(openssl rand -hex 24)
cleanup() {
    mysql -e "DROP DATABASE IF EXISTS $account; DROP USER IF EXISTS '$account'@'127.0.0.1';"
    runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS $account" >/dev/null
    runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "DROP ROLE IF EXISTS $account" >/dev/null
}
trap cleanup EXIT
mysql -e "CREATE DATABASE $account; CREATE USER '$account'@'127.0.0.1' IDENTIFIED BY '$password'; GRANT ALL ON *.* TO '$account'@'127.0.0.1';"
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "CREATE ROLE $account LOGIN CREATEDB PASSWORD '$password'" >/dev/null
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "CREATE DATABASE $account OWNER $account" >/dev/null
export TEST_MYSQL_DSN="$account:$password@tcp(127.0.0.1:3306)/$account?parseTime=true"
export TEST_POSTGRES_DSN="postgres://$account:$password@127.0.0.1:5432/$account?sslmode=disable"
../build/channel-cost/controller.test -test.run '^TestRevenueAppendIndependentProcesses$' -test.v -test.timeout 90s
