import * as jose from "jose";
import http from "node:http";

// startJwksServer generates an RS256 key pair and serves its public half as
// a JWKS on an ephemeral port, mirroring the shape pkg/cmd/server_test.go's
// newTestJWKSServer uses for the Go-side remote pairing tests (self-hosted
// JWKS, single key, kid-tagged).
export async function startJwksServer(kid) {
  const { publicKey, privateKey } = await jose.generateKeyPair("RS256", { extractable: true });
  const jwk = await jose.exportJWK(publicKey);
  jwk.kid = kid;
  jwk.alg = "RS256";
  jwk.use = "sig";
  const body = JSON.stringify({ keys: [jwk] });

  const server = http.createServer((req, res) => {
    res.setHeader("Content-Type", "application/json");
    res.end(body);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address();

  return {
    url: `http://127.0.0.1:${port}/jwks.json`,
    privateKey,
    close: () => new Promise((resolve) => server.close(resolve)),
  };
}

// signJWT issues an RS256 JWT for sub/issuer/audience, matching the claims
// shape identity.Resolver(source: jwt) expects (see pkg/services/identity).
// An optional jti lets callers mint distinct tokens for the same sub, e.g.
// to verify routing survives token rotation.
export async function signJWT({ privateKey, kid, sub, issuer, audience, jti }) {
  let builder = new jose.SignJWT({ sub })
    .setProtectedHeader({ alg: "RS256", kid })
    .setIssuer(issuer)
    .setAudience(audience)
    .setIssuedAt()
    .setExpirationTime("5m");
  if (jti) builder = builder.setJti(jti);
  return builder.sign(privateKey);
}
