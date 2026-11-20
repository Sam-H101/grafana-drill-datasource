#!/bin/sh
# Generates a throwaway CA and a server certificate for drill-tls on first
# start (into the drill-tls volume, never into the repo), then starts Drill.
# Development and tests only.
set -eu
D=/tls
if [ ! -f "$D/keystore.p12" ]; then
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=Drill dev CA" \
    -keyout "$D/ca.key" -out "$D/ca.crt" 2>/dev/null
  openssl req -newkey rsa:2048 -nodes -subj "/CN=drill-tls" \
    -keyout "$D/server.key" -out "$D/server.csr" 2>/dev/null
  printf 'subjectAltName=DNS:drill-tls,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n' > "$D/ext.cnf"
  openssl x509 -req -in "$D/server.csr" -CA "$D/ca.crt" -CAkey "$D/ca.key" -CAcreateserial \
    -days 3650 -extfile "$D/ext.cnf" -out "$D/server.crt" 2>/dev/null
  openssl pkcs12 -export -in "$D/server.crt" -inkey "$D/server.key" -name drill \
    -out "$D/keystore.p12" -passout pass:drill-dev-keystore
  # Only the CA certificate is meant to be read by others.
  rm -f "$D/ca.key" "$D/server.csr" "$D/ca.srl"
  chmod 600 "$D/server.key" "$D/keystore.p12"
  chmod 644 "$D/ca.crt" "$D/server.crt"
  chown -R drilluser "$D"
fi
exec su drilluser -s /bin/sh -c /opt/drill/bin/drill-embedded
