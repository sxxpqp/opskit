package app

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func renderMiddleware(c middlewareSettings) (middlewareBundle, error) {
	b := middlewareBundle{Files: map[string][]byte{}}
	services := map[string]any{}
	volumes := map[string]any{}
	count := 3
	if c.Engine == "redis" {
		count = 6
	}
	if c.Engine == "minio" {
		count = 4
	}
	addresses := make([]string, count)
	for i := 0; i < count; i++ {
		addresses[i] = fmt.Sprintf("%s%d", c.Engine, i+1)
		if c.Driver == "ssh" {
			addresses[i] = c.Hosts[i%len(c.Hosts)].InternalAddress
		}
	}
	endpoints := make([]string, count)
	for i, address := range addresses {
		port := map[string]int{"mysql": 3306, "redis": 6379, "minio": 9000}[c.Engine]
		if c.Engine == "redis" && c.Driver == "ssh" && i >= 3 {
			port = 6380
		}
		endpoints[i] = fmt.Sprintf("%s:%d", address, port)
		name := fmt.Sprintf("%s%d", c.Engine, i+1)
		volume := name + "-data"
		volumes[volume] = map[string]any{}
		s := map[string]any{"image": c.component().Image, "restart": "unless-stopped", "volumes": []string{volume + ":/data"}}
		if c.Driver == "ssh" {
			s["network_mode"] = "host"
		} else {
			s["hostname"] = name
		}
		environment := map[string]string{}
		s["environment"] = environment
		health := ""
		switch c.Engine {
		case "redis":
			environment["REDISCLI_AUTH"] = "${OPSKIT_CLUSTER_PASSWORD:?set middleware password environment}"
			command := fmt.Sprintf("umask 077; printf 'bind 0.0.0.0\\nport %d\\ncluster-enabled yes\\ncluster-config-file nodes.conf\\ncluster-node-timeout 5000\\nappendonly yes\\nappendfsync everysec\\ndir /data\\nrequirepass %%s\\nmasterauth %%s\\n' \"$$REDISCLI_AUTH\" \"$$REDISCLI_AUTH\" > /data/redis.conf; ", port)
			if c.Driver == "ssh" {
				command += fmt.Sprintf("printf 'cluster-announce-ip %s\\ncluster-announce-port %d\\ncluster-announce-bus-port %d\\n' >> /data/redis.conf; ", address, port, port+10000)
			}
			command += "exec redis-server /data/redis.conf"
			s["command"] = []string{"sh", "-ec", command}
			s["working_dir"] = "/data"
			health = fmt.Sprintf("redis-cli -p %d ping | grep -qx PONG", port)
		case "mysql":
			environment["MYSQL_ROOT_PASSWORD"] = "${OPSKIT_CLUSTER_PASSWORD:?set middleware password environment}"
			environment["MYSQL_ROOT_HOST"] = "%"
			s["volumes"] = []string{volume + ":/var/lib/mysql"}
			s["command"] = []string{fmt.Sprintf("--server-id=%d", i+1), "--gtid-mode=ON", "--enforce-gtid-consistency=ON", "--log-replica-updates=ON", "--binlog-format=ROW", "--report-host=" + address, "--report-port=3306", "--loose-group-replication-start-on-boot=ON"}
			health = "MYSQL_PWD=\"$$MYSQL_ROOT_PASSWORD\" mysql --protocol TCP -h127.0.0.1 -uroot -Nse 'SELECT 1' | grep -qx 1"
		case "minio":
			environment["MINIO_ROOT_USER"] = c.MinIO.Username
			environment["MINIO_ROOT_PASSWORD"] = "${OPSKIT_CLUSTER_PASSWORD:?set middleware password environment}"
			args := []string{"server", "--console-address", ":9001"}
			for _, host := range addresses {
				args = append(args, "http://"+host+":9000/data")
			}
			s["command"] = args
			health = "curl -fsS http://127.0.0.1:9000/minio/health/live >/dev/null"
		}
		s["healthcheck"] = map[string]any{"test": []string{"CMD-SHELL", health}, "interval": "5s", "timeout": "3s", "retries": 60, "start_period": "15s"}
		services[name] = s
		b.Nodes = append(b.Nodes, map[string]any{"service": name, "endpoint": endpoints[i], "host_index": i % max(1, len(c.Hosts))})
	}
	// Compose interpolation uses $$ for variables interpreted inside a container.
	toolsEnv := map[string]string{"OPSKIT_CLUSTER_PASSWORD": "${OPSKIT_CLUSTER_PASSWORD:?set middleware password environment}", "REDISCLI_AUTH": "${OPSKIT_CLUSTER_PASSWORD}", "MYSQL_PWD": "${OPSKIT_CLUSTER_PASSWORD}"}
	toolImage := c.component().Image
	bootstrapEntry := []string{"sh", "/scripts/bootstrap.sh"}
	checkEntry := []string{"sh", "/scripts/check.sh"}
	switch c.Engine {
	case "redis":
		b.Bootstrap = redisClusterBootstrap(endpoints)
		b.Check = redisClusterCheck(endpoints)
	case "mysql":
		toolImage = c.MySQL.ShellImage
		b.Bootstrap = mysqlClusterBootstrap(c.project(), addresses)
		b.Recovery = mysqlClusterRecovery(c.project(), addresses)
		routers := []string{"router"}
		if c.Driver == "ssh" {
			routers = addresses
		}
		b.Check = mysqlClusterCheck(c.project(), addresses, routers)
		bootstrapEntry = []string{"mysqlsh", "--js", "--file", "/scripts/bootstrap.js"}
		checkEntry = []string{"mysqlsh", "--js", "--file", "/scripts/check.js"}
		// Router is started only after metadata initialization, via its explicit profile.
		router := map[string]any{"image": c.MySQL.RouterImage, "restart": "unless-stopped", "profiles": []string{"router"}, "environment": map[string]string{"MYSQL_HOST": addresses[0], "MYSQL_PORT": "3306", "MYSQL_USER": "root", "MYSQL_PASSWORD": "${OPSKIT_CLUSTER_PASSWORD}", "MYSQL_INNODB_CLUSTER_MEMBERS": "3"}}
		if c.Driver == "ssh" {
			router["network_mode"] = "host"
		} else if c.PublishPort > 0 {
			router["ports"] = []string{fmt.Sprintf("%s:%d:6446", c.BindAddress, c.PublishPort)}
		}
		services["router"] = router
	case "minio":
		frontendPort := 9000
		if c.Driver == "ssh" {
			frontendPort = 9002
		}
		frontendConfig := fmt.Sprintf("global\n  log stdout format raw local0\ndefaults\n  mode http\n  timeout connect 5s\n  timeout client 60s\n  timeout server 60s\nfrontend s3\n  bind *:%d\n  default_backend minio\nbackend minio\n  balance roundrobin\n  option httpchk\n  http-check send meth GET uri /minio/health/live ver HTTP/1.1 hdr Host localhost\n  http-check expect status 200\n", frontendPort)
		for i, address := range addresses {
			resolution := ""
			if c.Driver == "docker" {
				resolution = " resolvers container_dns resolve-prefer ipv4 init-addr last,libc,none"
			}
			frontendConfig += fmt.Sprintf("  server minio%d %s:9000 check inter 2s fall 2 rise 1%s\n", i+1, address, resolution)
		}
		if c.Driver == "docker" {
			frontendConfig += "resolvers container_dns\n  nameserver docker 127.0.0.11:53\n  resolve_retries 3\n  timeout resolve 1s\n  timeout retry 1s\n  hold valid 10s\n"
		}
		b.Files["frontend.cfg"] = []byte(frontendConfig)
		frontend := map[string]any{"image": c.MinIO.FrontendImage, "restart": "unless-stopped", "volumes": []string{"./frontend.cfg:/usr/local/etc/haproxy/haproxy.cfg:ro"}}
		if c.Driver == "ssh" {
			frontend["network_mode"] = "host"
		} else {
			frontend["depends_on"] = []string{"minio1", "minio2", "minio3", "minio4"}
			if c.PublishPort > 0 {
				frontend["ports"] = []string{fmt.Sprintf("%s:%d:9000", c.BindAddress, c.PublishPort)}
			}
		}
		services["frontend"] = frontend
		b.Check = "set -eu\n"
		for _, endpoint := range endpoints {
			b.Check += "curl -fsS http://" + endpoint + "/minio/health/cluster >/dev/null\n"
		}
		b.Check += "echo 'MinIO all configured endpoints have write quorum'\n"
		frontends := []string{"frontend"}
		if c.Driver == "ssh" {
			frontends = addresses
		}
		for _, address := range frontends {
			b.Check += fmt.Sprintf("curl --retry 20 --retry-delay 1 --retry-all-errors -fsS http://%s:%d/minio/health/cluster >/dev/null\n", address, frontendPort)
		}
	}
	roles := []string{"bootstrap", "check"}
	if c.Engine == "mysql" {
		roles = append(roles, "recover")
	}
	for _, role := range roles {
		if role == "bootstrap" && b.Bootstrap == "" {
			continue
		}
		file := role + ".sh"
		entry := bootstrapEntry
		text := b.Bootstrap
		if role == "check" {
			entry = checkEntry
			text = b.Check
		}
		if role == "recover" {
			text = b.Recovery
			entry = []string{"mysqlsh", "--js", "--file", "/scripts/recover.js"}
		}
		if c.Engine == "mysql" {
			file = role + ".js"
		}
		b.Files[file] = []byte(text)
		service := map[string]any{"image": toolImage, "profiles": []string{"tools"}, "environment": toolsEnv, "entrypoint": entry, "volumes": []string{"./" + file + ":/scripts/" + file + ":ro"}}
		if c.Driver == "ssh" {
			service["network_mode"] = "host"
		}
		services[role] = service
	}
	compose := map[string]any{"services": services, "volumes": volumes}
	raw, err := yaml.Marshal(compose)
	if err != nil {
		return b, err
	}
	b.Files["compose.yaml"] = raw
	return b, nil
}

func redisClusterBootstrap(endpoints []string) string {
	firstHost, firstPort, _ := strings.Cut(endpoints[0], ":")
	preamble := fmt.Sprintf("set -eu\ninfo=$(redis-cli -h %s -p %s cluster info)\n", firstHost, firstPort)
	// Existing membership is never reset; a partially created cluster requires inspection.
	script := preamble + "if printf '%s' \"$info\" | grep -q 'cluster_state:ok'; then echo 'Existing Redis Cluster; checking instead of recreating'; exit 0; fi\n"
	script += "if ! printf '%s' \"$info\" | grep -q 'cluster_known_nodes:1'; then echo 'Redis has existing partial membership; inspect before retrying' >&2; exit 1; fi\n"
	script += "redis-cli --cluster create " + strings.Join(endpoints[:3], " ") + " --cluster-replicas 0 --cluster-yes\n"
	for i := 3; i < 6; i++ {
		primary := (i + 1) % 3
		host, port, _ := strings.Cut(endpoints[primary], ":")
		script += fmt.Sprintf("id=$(redis-cli -h %s -p %s cluster myid)\nredis-cli --cluster add-node %s %s --cluster-slave --cluster-master-id \"$id\"\n", host, port, endpoints[i], endpoints[0])
	}
	return script
}

func redisClusterCheck(endpoints []string) string {
	script := "set -eu\n"
	for _, endpoint := range endpoints {
		host, port, _ := strings.Cut(endpoint, ":")
		script += fmt.Sprintf("info=$(redis-cli -h %s -p %s cluster info)\nprintf '%%s' \"$info\" | grep -q 'cluster_state:ok'\nprintf '%%s' \"$info\" | grep -q 'cluster_slots_ok:16384'\nprintf '%%s' \"$info\" | grep -q 'cluster_known_nodes:6'\n", host, port)
	}
	script += "echo 'Redis: six members, all slots healthy'\n"
	return script
}

func mysqlClusterBootstrap(name string, addresses []string) string {
	return fmt.Sprintf(`shell.options.useWizards=false;
var password = os.getenv('OPSKIT_CLUSTER_PASSWORD');
var nodes = %s;
function target(host) { return {scheme:'mysql', user:'root', password:password, host:host, port:3306}; }
for (var i=0;i<nodes.length;i++) { dba.configureInstance(target(nodes[i]), {restart:false}); }
shell.connect(target(nodes[0]));
var cluster;
try { cluster=dba.getCluster('%s'); }
catch(e) { var stored=shell.getSession().runSql("SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name='mysql_innodb_cluster_metadata'").fetchOne()[0]; if(stored>0) { throw new Error('Existing InnoDB Cluster metadata; inspect quorum and use middleware recover for a complete outage'); } cluster=dba.createCluster('%s', {communicationStack:'MYSQL', gtidSetIsComplete:true}); }
var topology=cluster.status().defaultReplicaSet.topology;
for(var i=1;i<nodes.length;i++) { if(!topology[nodes[i]+':3306']) { cluster.addInstance(target(nodes[i]), {recoveryMethod:'clone'}); } }
print(JSON.stringify(cluster.status()));
`, jsonStringArray(addresses), strings.ReplaceAll(name, "-", "_"), strings.ReplaceAll(name, "-", "_"))
}

func mysqlClusterRecovery(name string, addresses []string) string {
	return fmt.Sprintf(`shell.options.useWizards=false;
var password=os.getenv('OPSKIT_CLUSTER_PASSWORD');
var nodes=%s;
function target(host) {return {scheme:'mysql',user:'root',password:password,host:host,port:3306};}
function recover() {
  var gtids=[]; var online=0;
  for(var i=0;i<nodes.length;i++) {
    shell.connect(target(nodes[i]));
    var row=shell.getSession().runSql("SELECT MEMBER_STATE FROM performance_schema.replication_group_members WHERE MEMBER_ID=@@server_uuid").fetchOne();
    if(row && row[0]!=='OFFLINE' && row[0]!=='ERROR') {online++;}
    gtids.push(shell.getSession().runSql('SELECT @@GLOBAL.gtid_executed').fetchOne()[0]);
  }
  if(online===nodes.length) {print('All members active; no complete-outage recovery requested'); return;}
  if(online!==0) {throw new Error('Some members remain active; refusing complete-outage recovery. Inspect quorum manually.');}
  var seed=-1;
  for(var i=0;i<nodes.length;i++) {
    shell.connect(target(nodes[i]));var latest=true;
    for(var j=0;j<gtids.length;j++) {if(shell.getSession().runSql('SELECT GTID_SUBSET(?,@@GLOBAL.gtid_executed)',[gtids[j]]).fetchOne()[0]!==1) {latest=false;}}
    if(latest) {seed=i;break;}
  }
  if(seed<0) {throw new Error('Divergent GTID sets; automatic recovery refused. No force or bootstrap override is used.');}
  shell.connect(target(nodes[seed]));
  var cluster=dba.rebootClusterFromCompleteOutage('%s');
  print(JSON.stringify(cluster.status()));
}
recover();
`, jsonStringArray(addresses), strings.ReplaceAll(name, "-", "_"))
}

func mysqlClusterCheck(name string, addresses, routers []string) string {
	return fmt.Sprintf(`var password=os.getenv('OPSKIT_CLUSTER_PASSWORD');
var nodes=%s;
var cluster=null;
for(var i=0;i<nodes.length;i++) { try { shell.connect({scheme:'mysql',user:'root',password:password,host:nodes[i],port:3306}); cluster=dba.getCluster('%s'); break; } catch(e) {} }
if(cluster===null) { throw new Error('No reachable InnoDB Cluster member'); }
var status=cluster.status();
print(JSON.stringify(status));
if(status.defaultReplicaSet.status!=='OK') { throw new Error('Cluster is not fully healthy'); }
var topology=status.defaultReplicaSet.topology;
if(Object.keys(topology).length!==3) { throw new Error('Expected three cluster members'); }
for(var key in topology) { if(topology[key].status!=='ONLINE') { throw new Error('A member is not ONLINE'); } }
var routers=%s;
for(var r=0;r<routers.length;r++) {
  var ready=false;
  for(var attempt=0;attempt<60;attempt++) {
    try { shell.connect({scheme:'mysql',user:'root',password:password,host:routers[r],port:6446}); var row=shell.getSession().runSql('SELECT @@read_only').fetchOne(); if(row[0]!==0) {throw new Error('Router did not route to writable primary');} ready=true; break; } catch(e) { os.sleep(2); }
  }
  if(!ready) {throw new Error('Router write endpoint not ready: '+routers[r]);}
}
print('All Router write endpoints route to a primary');
`, jsonStringArray(addresses), strings.ReplaceAll(name, "-", "_"), jsonStringArray(routers))
}

func jsonStringArray(values []string) string {
	// Validated host names and IPv4 addresses contain no quoting characters.
	return "['" + strings.Join(values, "','") + "']"
}
