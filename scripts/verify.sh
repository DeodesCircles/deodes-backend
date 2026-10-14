#!/bin/bash
set -e

NETWORK="${1:-testnet}"

echo "=== Deodes Contract Verification ==="
echo "Network: $NETWORK"
echo ""

if [ "$NETWORK" = "mainnet" ]; then
  CHAIN_ID=5042
  RPC_URL="https://rpc.mainnet.arc.io"
  DEPLOY_FILE="../deodes-contracts/deployments.mainnet.json"
else
  CHAIN_ID=5042002
  RPC_URL="https://rpc.testnet.arc.io"
  DEPLOY_FILE="../deodes-contracts/deployments.testnet.json"
fi

echo "--- Foundry build + tests ---"
(cd ../deodes-contracts && forge build && forge test)
echo ""
echo "--- ABI publication check ---"
(cd ../deodes-contracts && ./script/publish-abi.sh --check)
echo ""
echo "--- Deployment records ---"
if [ -f "$DEPLOY_FILE" ]; then
  python3 -c "import json; [print(' ', d['contract'], d['address']) for d in json.load(open('$DEPLOY_FILE')).get('deployments', [])]"
else
  echo "  ✗ $DEPLOY_FILE — NOT FOUND (run the CEREMONY.md broadcast first)"
fi
echo ""
echo "To verify on-chain state:"
echo "  cast chain-id --rpc-url $RPC_URL  # want $CHAIN_ID"
echo "  cast call <CIRCLE> \"memberCount()(uint256)\" --rpc-url \$RPC_URL"
