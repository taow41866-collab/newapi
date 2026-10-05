#!/bin/sh
# Disposable WSL databases only; does not access production.
set -eu
cd /mnt/f/Projects/newapi/controller
account="cdtest_$(date +%s)"
password=$(openssl rand -hex 24)
cleanup() {
    mysql -e "DROP DATABASE IF EXISTS $account; DROP USER IF EXISTS '$account'@'127.0.0.1';"
    runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "DROP DATABASE IF EXISTS $account" >/dev/null
    runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "DROP ROLE IF EXISTS $account" >/dev/null
    test "$(mysql -N -e "SELECT COUNT(*) FROM mysql.user WHERE user='$account'")" = 0
    test "$(runuser -u postgres -- psql -At -c "SELECT COUNT(*) FROM pg_roles WHERE rolname='$account'")" = 0
    printf '%s\n' 'Disposable database accounts removed and verified.'
}
trap cleanup EXIT
mysql -e "CREATE DATABASE $account; CREATE USER '$account'@'127.0.0.1' IDENTIFIED BY '$password'; GRANT ALL ON $account.* TO '$account'@'127.0.0.1'; GRANT ALL ON \`newapi_audit_%\`.* TO '$account'@'127.0.0.1';"
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "CREATE ROLE $account LOGIN CREATEDB PASSWORD '$password'" >/dev/null
runuser -u postgres -- psql -v ON_ERROR_STOP=1 -c "CREATE DATABASE $account OWNER $account" >/dev/null
export TEST_MYSQL_DSN="$account:$password@tcp(127.0.0.1:3306)/$account?parseTime=true"
export TEST_POSTGRES_DSN="postgres://$account:$password@127.0.0.1:5432/$account?sslmode=disable"
../build/customer-discount/controller.test -test.run '^TestCustomerChannelDiscountDatabaseMatrix$' -test.v -test.timeout 120s
../build/customer-discount/service.test -test.run '^TestFixedPriceBillingDatabaseMatrix$' -test.v -test.timeout 120s
for dialect in mysql postgres; do
    export TEST_TASK_DB_DIALECT="$dialect"
    ../build/customer-discount/controller.test -test.run '^TestImmediateTaskSettlementDatabase$' -test.v -test.timeout 90s
done
