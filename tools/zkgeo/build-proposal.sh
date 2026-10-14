VK=$(base64 < ../../keys-geo/vk.bin | tr -d '\n')
GOV=$(lucidchaind q auth module-account gov -o json | jq -r '.account.value.address // .account.address')
jq -n --arg vk "$VK" --arg gov "$GOV" '{
  messages: [{
    "@type": "/lucidchain.proofs.v1.MsgRegisterVerifier",
    authority: $gov, proof_system_id: "plonk-bn254",
    description: "zkgeo batch geofence (dev)",
    verification_key: $vk, verification_gas: "500000"
  }],
  metadata: "", deposit: "10000000stake",
  title: "Register zkgeo verifier", summary: "Dev registration", expedited: false
}' > proposal.json
