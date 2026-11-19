#!/usr/bin/env bash
# Single-denom + x402-readiness patch for a freshly initialised lucidchain home.
#
# Run it right after `lucidchaind init` and BEFORE `genesis add-genesis-account`
# and `genesis gentx`, so that every module default (bond denom, mint denom, gov
# deposits, module fee denoms) switches to the final denom while balances are
# still empty. It refuses to run if balances or supply already exist.
#
# Required environment (no defaults on purpose, so nothing is guessed):
#   DENOM           final base denom, e.g. ucheck
#   DISPLAY_DENOM   human unit, e.g. check   (6 decimals assumed below)
#   SYMBOL          e.g. CHECK
#   NAME            e.g. Check
#   MAX_GAS         block max_gas from your profiling; must exceed your heaviest tx
#   MIN_GAS_PRICE   decimal price per gas unit, from profiling, e.g. 0.025
# Optional:
#   HOME_DIR        default ~/.lucidchain
#   TIMEOUT_COMMIT  default 1s
#   DISABLE_ICA_HOST default 1 (set 0 to leave ICA host as generated)
#   EXPONENT        default 6
#
# Untested against a real `lucidchaind` here: run `lucidchaind genesis validate`
# afterwards, and confirm your CometBFT version still honours timeout_commit.
set -euo pipefail

: "${DENOM:?set DENOM, e.g. DENOM=ucheck}"
: "${DISPLAY_DENOM:?set DISPLAY_DENOM, e.g. check}"
: "${SYMBOL:?set SYMBOL, e.g. CHECK}"
: "${NAME:?set NAME, e.g. Check}"
: "${MAX_GAS:?set MAX_GAS from profiling}"
: "${MIN_GAS_PRICE:?set MIN_GAS_PRICE from profiling}"

HOME_DIR="${HOME_DIR:-$HOME/.lucidchain}"
TIMEOUT_COMMIT="${TIMEOUT_COMMIT:-1s}"
DISABLE_ICA_HOST="${DISABLE_ICA_HOST:-1}"
EXPONENT="${EXPONENT:-6}"

G="$HOME_DIR/config/genesis.json"
APP="$HOME_DIR/config/app.toml"
CMT="$HOME_DIR/config/config.toml"

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }
for f in "$G" "$APP" "$CMT"; do [ -f "$f" ] || { echo "missing $f" >&2; exit 1; }; done
[[ "$MAX_GAS" =~ ^[0-9]+$ ]] || { echo "MAX_GAS must be an integer" >&2; exit 1; }
[[ "$DENOM" =~ ^[a-zA-Z][a-zA-Z0-9/:._-]{2,127}$ ]] || { echo "invalid DENOM" >&2; exit 1; }

# Guard: replacing denoms after balances exist could create duplicate coins.
jq -e '(.app_state.bank.balances | length) == 0 and (.app_state.bank.supply | length) == 0' "$G" >/dev/null \
  || { echo "bank balances/supply are not empty: run this before add-genesis-account" >&2; exit 1; }

cp "$G" "$G.bak"

jq \
  --arg d "$DENOM" --arg disp "$DISPLAY_DENOM" --arg sym "$SYMBOL" --arg name "$NAME" \
  --argjson exp "$EXPONENT" --arg maxgas "$MAX_GAS" --argjson noica "$DISABLE_ICA_HOST" '
  # 1. every value that is exactly the default bond denom becomes the final denom
  walk(if . == "stake" then $d else . end)

  # 2. one denom, with metadata (no standard bank message sets this after launch)
  | .app_state.bank.denom_metadata = [{
      description: ($name + " native token"),
      denom_units: [
        {denom: $d,    exponent: 0,    aliases: []},
        {denom: $disp, exponent: $exp, aliases: []}
      ],
      base: $d, display: $disp, name: $name, symbol: $sym
    }]

  # 3. finite block gas (consensus params live at the top level of this genesis)
  | .consensus.params.block.max_gas = $maxgas

  # 4. optionally disable the ICA host so remote chains cannot run arbitrary messages
  | if $noica == 1
      then .app_state.interchainaccounts.host_genesis_state.params.host_enabled = false
      else . end
' "$G" > "$G.tmp" && mv "$G.tmp" "$G"

# app.toml: gas price floor in the single denom, bounded query gas
sed -i "s|^minimum-gas-prices = .*|minimum-gas-prices = \"${MIN_GAS_PRICE}${DENOM}\"|" "$APP"
sed -i 's|^query-gas-limit = .*|query-gas-limit = "50000000"|' "$APP"

# config.toml: faster blocks (verify this key is still honoured by your CometBFT)
sed -i "s|^timeout_commit = .*|timeout_commit = \"${TIMEOUT_COMMIT}\"|" "$CMT"

echo "Patched $G (backup: $G.bak), $APP and $CMT"
echo "Next: genesis add-genesis-account / gentx / collect-gentxs using ${DENOM}, then: lucidchaind genesis validate"
echo "Remember to replace every 'stake'/'ucheck' in runbook commands (amounts and --gas-prices) with ${DENOM}."
