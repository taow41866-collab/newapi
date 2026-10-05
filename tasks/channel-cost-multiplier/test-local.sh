#!/bin/sh
set -eu
cd /mnt/f/Projects/newapi/controller
../build/channel-cost/controller.test -test.run 'Test(RevenueHistory|RevenueAppend|AppendPurchasePrice|CalculateRevenue|ValidateRevenue|GetRevenue|RevenueUsage)' -test.v -test.timeout 90s
