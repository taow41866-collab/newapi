# Customer Discount Database Evidence

Local verification only; no production access, paid requests, pushes, or deployment.

## Environment

- Windows Go 1.27.1, cross-compiled with `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`.
- Existing WSL distribution `Ubuntu-2404`; Docker Desktop was not started.
- SQLite 3.50.4, MySQL 8.0.46-0ubuntu0.24.04.4, PostgreSQL 16.15 (Ubuntu 16.15-0ubuntu0.24.04.1).
- Loopback-only databases and random disposable accounts; credentials are not logged.

## Commands And Results

```powershell
$env:GOOS='linux'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
go test -c ./controller -o build/channel-cost/controller.test
wsl -d Ubuntu-2404 -- bash -lc 'sh /mnt/f/Projects/newapi/tasks/customer-channel-discount/test-local-matrix.sh'
```

At 2026-10-05 19:25:55 Asia/Shanghai, both commands exited 0. Linux test binary SHA256 was `51e72301b4171e27fbad05916405488af57206df2c5d5ba101ff1abda1bbc01b`.

`TestCustomerChannelDiscountDatabaseMatrix` passed on all three engines:

- Two separate test processes concurrently append the first rule; one immutable version remains, both processes resolve the persisted rule, and the loser receives an expected version conflict.
- Repeated `AutoMigrate` preserves the rule JSON without changing the value.
- New task rows preserve the customer/channel multiplier, rule version/time, normal price, request multipliers, expression and usage facts after persistence and two migrations.
- Released-shape task JSON without a discount snapshot remains readable, has no discount, and retains quota, upstream task ID and pricing quantities after two migrations.

The first run exposed a test-fixture mistake: reusing a populated GORM destination retained the new task primary key when reading the legacy task. Clearing the destination fixed the fixture; the complete three-engine matrix then passed. No business implementation was changed for that failure.

## Scope And Limits

The task table fields and GORM tags match published commit `fe6b323a4b32022b40fd4a41a1275275a11d9741`; the new snapshot is inside the existing JSON column and introduces no table/schema change. The upgrade test seeds the released JSON shape in that unchanged table schema, not a full production database restore.

This is a targeted database contract test, not full Linux backend regression, complete billing/end-to-end coverage, lowest-supported MySQL/PostgreSQL version testing, CI, or production acceptance. Rebuild and rerun after any relevant source changes.
