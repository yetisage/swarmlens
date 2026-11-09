# Recorded Docker API responses

These are responses of a Docker 29.9.2 swarm (API 1.51, which is what the
client negotiates), recorded with `scripts/record-fixtures.py`. The tests in
`../../inspect_test.go` and `../../tls_test.go` serve them from a fake daemon and
run the real code against them; the describe and YAML reports made from them are
the golden files in `../golden`.

The swarm they came from is a single manager holding:

- the stack `shop`: `shop_api` (2 replicas, environment, a secret, a config,
  a published port, an overlay network) and `shop_worker`, an overlay network
  `shop_back`, the secret `shop_dbpass` and the config `shop_appcfg`
- the standalone service `web` (2 replicas, a label, a memory limit)
- the service `broken`, whose image cannot be pulled, so its only task is
  `rejected` with a registry error
- the volume `shop_data`, labelled as part of `shop`

Nothing in them is sensitive (the environment values are made up, there are no
certificates, keys or join tokens), and the daemon's data root has been
replaced by `/var/lib/docker`.

## Recording new ones

```sh
docker swarm init --advertise-addr 127.0.0.1

# a tiny image that needs no registry: a static program that logs a coloured
# line every second
mkdir hello && cd hello
cat > main.go <<'EOF'
package main

import ("fmt"; "os"; "time")

func main() {
	for i := 0; ; i++ {
		fmt.Printf("\x1b[32mINFO\x1b[0m tick %d\n", i)
		if i%3 == 0 {
			fmt.Fprintf(os.Stderr, "\x1b[31mERROR\x1b[0m odd tick %d\n", i)
		}
		time.Sleep(time.Second)
	}
}
EOF
printf 'module hello\ngo 1.22\n' > go.mod
CGO_ENABLED=0 go build -o hello . && tar -cf hello.tar hello
docker import hello.tar hello:1 --change 'CMD ["/hello"]'
cd ..

# the stack
echo 'key=value' > app.conf; printf 'hunter2' > db.pass
cat > compose.yml <<'EOF'
services:
  api:
    image: hello:1
    deploy: {replicas: 2}
    environment: ["TOKEN=abc123", "ESC=pwn"]
    networks: [back]
    configs: [appcfg]
    secrets: [dbpass]
    ports: ["8080:80"]
  worker:
    image: hello:1
    deploy: {replicas: 1}
configs:
  appcfg: {file: ./app.conf}
secrets:
  dbpass: {file: ./db.pass}
networks:
  back: {}
EOF
docker stack deploy -c compose.yml shop
docker service create --name web --replicas 2 --label owner=review --limit-memory 64m --detach hello:1
docker service create --name broken --restart-condition none --detach no-such-registry.invalid/ghost:1
docker volume create --label com.docker.stack.namespace=shop shop_data
sleep 10

python3 scripts/record-fixtures.py internal/api/testdata/docker-29.9.2
go test ./internal/api -run Recorded -update     # then read the diff of testdata/golden
```

`frozen` in `inspect_test.go` is the instant the reports are made at: keep it a
little after the newest timestamp in the fixtures, or every age reads `0s`.
