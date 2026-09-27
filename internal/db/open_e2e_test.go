package db

import (
	"bytes"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/jackc/pgx/v5"
)

// 本文件补的是「生产启动路径」那一格：OpenMySQL / OpenPG 里拼 DSN、设连接参数、
// 建 schema、跑迁移的那段代码。同目录的 e2e 套件（mysqlTestDB / pgTestDB）为了
// 隔离，自己 sql.Open + MigrateForTest 就把表建好了，从没走过 Open*，于是
// maxAllowedPacket=0 / parseTime&loc=Local / collation / search_path /
// registerLocalTimestamp 这些「配错了不报错、只在生产上表现成静默数据错误」的
// 参数，长期是零覆盖。
//
// 断言一律写在参数的实际效果上（会话变量、时间往返、current_schema），不复刻
// 一遍常量或自己拼 DSN 再解析回来 —— 那是循环论证，证明了等于没证。

// mysqlOpenConfig 从 DB_TEST_MYSQL_DSN 取出 OpenMySQL 需要的连接信息，并自建一个
// 一次性 database 供用例使用。
//
// 不复用 DSN 里那个库：e2e 套件每次运行都新建 any_llm_test_<nano> 并 drop，DSN 指向
// 的基础库因此长期空置；而 OpenMySQL 会往里建表，用例再往表里插行的话，同一个库会
// 跨运行累积状态 —— 上一次失败的残留行会让这一次以 "Duplicate entry" 挂掉，属于
// 测试自身的隔离缺陷而不是被测代码的问题。
func mysqlOpenConfig(t *testing.T) MySQLConfig {
	t.Helper()
	dsn := os.Getenv("DB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_MYSQL_DSN to run mysql e2e tests")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	host, portStr, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		t.Fatalf("split addr %q: %v", cfg.Addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("bad port %q: %v", portStr, err)
	}

	// 控制连接：连到 DSN 里原有的库，负责建/删本次的库。必须活到清理那一刻
	// （t.Cleanup 后进先出 ⇒ 业务连接先关），否则 DROP 打在已关闭的连接上。
	ctrlCfg := *cfg
	ctrl, err := sql.Open("mysql", ctrlCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open control conn: %v", err)
	}
	if err := ctrl.Ping(); err != nil {
		ctrl.Close()
		t.Fatalf("ping control conn: %v", err)
	}
	dbName := fmt.Sprintf("any_llm_open_test_%d", time.Now().UnixNano())
	if _, err := ctrl.Exec(fmt.Sprintf(
		"CREATE DATABASE %s DEFAULT CHARACTER SET utf8mb4 COLLATE utf8mb4_bin", dbName)); err != nil {
		ctrl.Close()
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := ctrl.Exec("DROP DATABASE " + dbName); err != nil {
			t.Errorf("drop database %s: %v", dbName, err)
		}
		ctrl.Close()
	})

	return MySQLConfig{
		Host:     host,
		Port:     port,
		User:     cfg.User,
		Password: cfg.Passwd,
		DBName:   dbName,
	}
}

// sqlNowSecond 返回当前时间并对齐到秒：MySQL 的 DATETIME(0) 与 PG 的 TIMESTAMP(0)
// 都是把小数秒**四舍五入**（不是截断）到秒，所以基准必须同样取整，否则纳秒 >= 0.5s
// 的那一半运行会假失败。
func sqlNowSecond() time.Time { return time.Now().Round(time.Second) }

// TestMySQL_E2E_OpenAppliesDSNParams 走 OpenMySQL，断言 DSN 参数的实际效果。
// 这三项任一配错都不会在这一层报错，只会让唯一性比较静默变宽松、或让 64 MiB
// 的归档必然写失败（失败还只落在异步 writer 的日志里）。
func TestMySQL_E2E_OpenAppliesDSNParams(t *testing.T) {
	cfg := mysqlOpenConfig(t)

	d, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	if got := DialectOf(d); got != DialectMySQL {
		t.Fatalf("DialectOf=%q want mysql", got)
	}

	// collation：MySQL 默认的 utf8mb4_0900_ai_ci 大小写不敏感，会让 name/key 的
	// 唯一性偏离 PG/SQLite 的字节精确比较。utf8mb4_bin 才是本项目的语义。
	var collation string
	if err := d.QueryRow(`SELECT @@collation_connection`).Scan(&collation); err != nil {
		t.Fatalf("read collation_connection: %v", err)
	}
	if collation != "utf8mb4_bin" {
		t.Errorf("collation_connection=%q want utf8mb4_bin", collation)
	}

	// maxAllowedPacket=0 这一项**没有断言**，原因值得写下来。
	//
	// 想用行为证它，试过两种办法都不成立：
	//  1. 对比 @@max_allowed_packet：那是服务器自己的值，客户端配多少它都不变，
	//     纯空测。
	//  2. 发一个 >64 MiB（驱动默认值）的载荷：也不报错。驱动只在**单个包**超过
	//     上限时才回 ErrPktTooLarge（packets.go: `pktLen > mc.maxAllowedPacket`），
	//     而大参数会按 `maxAllowedPacket/(paramCount+1)` 分片流式发送，每片都远小于
	//     上限。变异验证过：把 OpenMySQL 里那一行改成 64<<20，整套用例依然全绿。
	//
	// 结论：在当前驱动版本下这个字段的取值无法从外部观测区分；它的实际作用是
	// 「显式声明意图」（connector.go: 0 → 握手后读服务器的 @@max_allowed_packet）。
	// 与其写一条「自己赋值、自己断言」的同义反复，不如把结论留在注释里。
}

// TestMySQL_E2E_OpenConnectsAndMigrates 证明 OpenMySQL 用生产那套参数确实能连上
// 并跑完迁移。这是上面那些 DSN 行为断言的地基：参数再对，连不上也是白搭。
func TestMySQL_E2E_OpenConnectsAndMigrates(t *testing.T) {
	cfg := mysqlOpenConfig(t)

	d, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	defer d.Close()

	var one int
	if err := d.QueryRow(`SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("SELECT 1: n=%d err=%v", one, err)
	}
	// 迁移确实跑过：upstreams 表在本次自建的库里。
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_name = 'upstreams'`).Scan(&n); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if n != 1 {
		t.Errorf("upstreams 未建在 %s 下（n=%d）", cfg.DBName, n)
	}
}

// TestMySQL_E2E_OpenTimestampRoundTrip 钉住 parseTime=true&loc=Local 的效果。
//
// 用 time.Now() 而不是自造的固定偏移时间：DATETIME(0) 本身不带时区，只比 Go
// 侧的绝对时刻的话，「墙钟守恒」和「loc 生效」两种行为在跨时区时都能让
// Equal() 成立（写 05:06:07+08 → 读 21:06:07Z，绝对时刻相同），断言会退化成
// 空测。真正要证的是 driver 把墙钟按 time.Local 解释，所以必须拿真实的内存
// 值当基准：本机非 UTC 时，漏掉 loc=Local 会让存进去的墙钟整体偏移，
// 下面两条即刻失败。
func TestMySQL_E2E_OpenTimestampRoundTrip(t *testing.T) {
	cfg := mysqlOpenConfig(t)

	d, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	want := sqlNowSecond()
	// 直接写 SQL 而不是走 store.CreateUpstream：后者用 time.Now() 覆盖调用方
	// 传的时间，喂不进确定值。
	if _, err := d.Exec(Rebind(d, `INSERT INTO upstreams
		(name, base_url, api_key, format, created_at, updated_at) VALUES (?,?,?,?,?,?)`),
		"ts-roundtrip", "https://example.invalid", "sk-e2e", "openai", want, want); err != nil {
		t.Fatalf("insert upstream: %v", err)
	}

	// (1) 库里的墙钟必须等于 time.Local 下的墙上时间。
	var stored string
	if err := d.QueryRow(`SELECT DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s')
		FROM upstreams WHERE name = 'ts-roundtrip'`).Scan(&stored); err != nil {
		t.Fatalf("select formatted created_at: %v", err)
	}
	wantWall := want.In(time.Local).Format("2006-01-02 15:04:05")
	if stored != wantWall {
		t.Errorf("库中墙钟=%q want %q（loc 未按 time.Local 解释）", stored, wantWall)
	}

	// (2) 扫行路径读回来。这里必须 Scan 进 time.Time：parseTime=false 时驱动
	// 给出 []byte，本行会直接报错，而生产里 store 的每个上游查询都是这么扫的。
	// 不能借 store.GetUpstreamByID —— store 反向 import db，同包测试里引用即环。
	var got time.Time
	if err := d.QueryRow(`SELECT created_at FROM upstreams WHERE name = 'ts-roundtrip'`).Scan(&got); err != nil {
		t.Fatalf("scan created_at into time.Time（parseTime=false 时这里会因 []byte 报错）: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("created_at 往返后=%v want %v", got, want)
	}
}

// TestMySQL_E2E_OpenWarnsOnSmallMaxAllowedPacket 覆盖 warnIfSmallMaxAllowedPacket。
// 写入走异步 writer、失败只记日志，启动时这句告警是唯一能让运维看见的地方，
// 所以阈值两侧（<128 MiB 告警 / 充足不告警）都要钉住。
func TestMySQL_E2E_OpenWarnsOnSmallMaxAllowedPacket(t *testing.T) {
	cfg := mysqlOpenConfig(t)

	probe, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}
	t.Cleanup(func() { probe.Close() })

	var orig int64
	if err := probe.QueryRow(`SELECT @@max_allowed_packet`).Scan(&orig); err != nil {
		t.Fatalf("read @@max_allowed_packet: %v", err)
	}
	if _, err := probe.Exec("SET GLOBAL max_allowed_packet = 1048576"); err != nil {
		t.Skipf("cannot SET GLOBAL max_allowed_packet: %v", err)
	}
	t.Cleanup(func() {
		if _, err := probe.Exec(fmt.Sprintf("SET GLOBAL max_allowed_packet = %d", orig)); err != nil {
			t.Logf("restore max_allowed_packet: %v", err)
		}
	})

	// 小上限 → 必须告警。captureLogs 换成内存缓冲，测试结束还原包级 logger。
	logs, restore := captureLogs(t)
	defer restore()
	d, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL with small packet: %v", err)
	}
	d.Close()
	if !strings.Contains(logs.String(), "max_allowed_packet") {
		t.Errorf("max_allowed_packet 过小时未告警；logs=%q", logs.String())
	}

	// 上限充足 → 不该告警（阈值另一侧）。
	if _, err := probe.Exec(fmt.Sprintf("SET GLOBAL max_allowed_packet = %d", 256<<20)); err != nil {
		t.Skipf("cannot raise max_allowed_packet: %v", err)
	}
	logs.Reset()
	d2, err := OpenMySQL(cfg)
	if err != nil {
		t.Fatalf("OpenMySQL with large packet: %v", err)
	}
	d2.Close()
	if strings.Contains(logs.String(), "max_allowed_packet") {
		t.Errorf("max_allowed_packet 充足时仍告警；logs=%q", logs.String())
	}
}

// captureLogs 把包级 logger 的默认输出换成内存缓冲，返回缓冲与还原函数。
// 必须还原：包级 logger 是全局状态，留着会污染后续用例的输出。
func captureLogs(t *testing.T) (*bytes.Buffer, func()) {
	t.Helper()
	prev := logger.Default()
	buf := &bytes.Buffer{}
	logger.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return buf, func() { logger.SetDefault(prev) }
}

// TestMySQL_E2E_OpenPingFailure 覆盖 OpenMySQL 的 Ping 失败分支：连不通时必须
// 报出 "ping mysql" 并关掉连接，而不是把半成品 *sql.DB 交给调用方。
func TestMySQL_E2E_OpenPingFailure(t *testing.T) {
	cfg := mysqlOpenConfig(t)
	cfg.Host = "127.0.0.1"
	cfg.Port = 1 // 保留端口，正常不会有 mysqld 监听

	d, err := OpenMySQL(cfg)
	if err == nil {
		d.Close()
		t.Fatal("OpenMySQL 对不可达端口返回 nil error")
	}
	if !strings.Contains(err.Error(), "ping mysql") {
		t.Errorf("err=%v want 含 \"ping mysql\"", err)
	}
}

// pgOpenConfig 与 mysqlOpenConfig 对应，从 DB_TEST_PG_DSN 还原 PGConfig。
func pgOpenConfig(t *testing.T) PGConfig {
	t.Helper()
	dsn := os.Getenv("DB_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set DB_TEST_PG_DSN to run postgres e2e tests")
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	return PGConfig{
		Host:     parsed.Host,
		Port:     int(parsed.Port),
		User:     parsed.User,
		Password: parsed.Password,
		DBName:   parsed.Database,
	}
}

// TestPG_E2E_OpenCreatesSchemaAndMigrates 走 OpenPG 的全过程：schema 不存在时
// 由 OpenPG 自己建（调用方不必先建），迁移后的表挂在那个 schema 下，时间戳
// 带本地偏移（registerLocalTimestamp 生效），且重复调用是幂等的。
func TestPG_E2E_OpenCreatesSchemaAndMigrates(t *testing.T) {
	base := pgOpenConfig(t)
	schema := fmt.Sprintf("any_llm_open_test_%d", time.Now().UnixNano())
	cfg := base
	cfg.Schema = schema

	// 控制连接负责清理：OpenPG 建的表都在 `schema` 下，DROP SCHEMA CASCADE 足够。
	ctrl, err := OpenPG(base)
	if err != nil {
		t.Fatalf("OpenPG control: %v", err)
	}
	t.Cleanup(func() {
		if _, err := ctrl.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema)); err != nil {
			t.Errorf("drop schema: %v", err)
		}
		ctrl.Close()
	})

	d, err := OpenPG(cfg)
	if err != nil {
		t.Fatalf("OpenPG: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	if got := DialectOf(d); got != DialectPostgres {
		t.Fatalf("DialectOf=%q want postgres", got)
	}

	// schema 必须被 search_path 选中（OpenPG 靠 RuntimeParams 设，不是 SET ——
	// 连接池里 SET 只影响单条连接）。
	var current string
	if err := d.QueryRow(`SELECT current_schema()`).Scan(&current); err != nil {
		t.Fatalf("current_schema: %v", err)
	}
	if current != schema {
		t.Errorf("current_schema=%q want %q", current, schema)
	}

	// 迁移确实落在这个 schema 里。
	var n int
	if err := d.QueryRow(Rebind(d, `SELECT COUNT(*) FROM information_schema.tables
		WHERE table_schema = ? AND table_name = 'upstreams'`), schema).Scan(&n); err != nil {
		t.Fatalf("count tables: %v", err)
	}
	if n != 1 {
		t.Errorf("upstreams 未建在 %s 下（n=%d）", schema, n)
	}

	// timestamp codec：注册后扫出的时间带本地偏移，而不是 UTC。
	// 注意这条断言在「主机时区恰为 UTC」时会退化成恒真 —— CI runner 就是 UTC，
	// 所以它主要保护非 UTC 的开发/生产环境；要彻底钉死得固定容器时区，
	// 代价大于收益。这里如实记下这个局限。
	want := sqlNowSecond()
	if _, err := d.Exec(Rebind(d, `INSERT INTO upstreams
		(name, base_url, api_key, format, created_at, updated_at) VALUES (?,?,?,?,?,?)`),
		"pg-open-ts", "https://example.invalid", "sk-e2e", "openai", want, want); err != nil {
		t.Fatalf("insert upstream: %v", err)
	}
	var got time.Time
	if err := d.QueryRow(`SELECT created_at FROM upstreams WHERE name = 'pg-open-ts'`).Scan(&got); err != nil {
		t.Fatalf("select created_at: %v", err)
	}
	_, wantOffset := want.Zone()
	if _, gotOffset := got.Zone(); gotOffset != wantOffset {
		t.Errorf("created_at offset=%d want %d（registerLocalTimestamp 未生效）", gotOffset, wantOffset)
	}
	if !got.Equal(want) {
		t.Errorf("created_at=%v want %v", got, want)
	}

	// 幂等：OpenPG 可重复调用（CREATE SCHEMA IF NOT EXISTS + 迁移幂等）。
	d2, err := OpenPG(cfg)
	if err != nil {
		t.Fatalf("OpenPG 二次调用失败（应幂等）: %v", err)
	}
	d2.Close()
}

// TestPG_E2E_OpenInvalidSchemaName 是安全边界：schema 名会被拼进
// `CREATE SCHEMA IF NOT EXISTS %s`，所以必须先过标识符白名单，否则是注入口。
func TestPG_E2E_OpenInvalidSchemaName(t *testing.T) {
	cfg := pgOpenConfig(t)

	for _, bad := range []string{
		`public; DROP TABLE upstreams`,
		`has space`,
		`1leading_digit`,
		`quote"inside`,
		`dash-in-name`,
	} {
		cfg.Schema = bad
		d, err := OpenPG(cfg)
		if err == nil {
			// 校验在 OpenDB 之前，正常拿不到连接；防御性关一下。
			d.Close()
			t.Errorf("schema %q 被接受，应为非法标识符", bad)
			continue
		}
		if !strings.Contains(err.Error(), "invalid schema name") {
			t.Errorf("schema %q: err=%v want 含 \"invalid schema name\"", bad, err)
		}
	}
}

// TestPG_E2E_OpenPingFailure 覆盖 OpenPG 的 Ping 失败分支。
func TestPG_E2E_OpenPingFailure(t *testing.T) {
	cfg := pgOpenConfig(t)
	cfg.Host = "127.0.0.1"
	cfg.Port = 1

	d, err := OpenPG(cfg)
	if err == nil {
		d.Close()
		t.Fatal("OpenPG 对不可达端口返回 nil error")
	}
	if !strings.Contains(err.Error(), "ping postgres") {
		t.Errorf("err=%v want 含 \"ping postgres\"", err)
	}
}
