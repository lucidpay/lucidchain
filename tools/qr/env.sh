# Source this file to work with the quantum-resistant (ML-DSA) chain:
#   source tools/qr/env.sh
#
# It points lucidchaind at a separate node home so the classical dev chain in
# ~/.lucidchain and its keyring are never touched. Run it again in every new
# terminal. Run `qr-off` to go back to the classical dev setup.
#
# Only one chain can run at a time (both use the default ports).

export QR_HOME="${QR_HOME:-$HOME/.lucidchain-qr}"

lucidchaind() { command lucidchaind --home "$QR_HOME" "$@"; }

qr-off() {
  unset -f lucidchaind qr-off
  unset QR_HOME
  echo "QR mode off: lucidchaind now uses its default home."
}

echo "QR mode on: lucidchaind --home $QR_HOME"
