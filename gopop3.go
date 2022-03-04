package gopop3

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

// Client 实现了一个电子邮件客户端。
type Client struct {
	opt Option
}

// Conn 是与 POP3 服务器的有状态连接
type Conn struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

// Option 代表客户端配置。
type Option struct {
	Host string `json:"host"`
	Port int    `json:"port"`

	// Default is 3 seconds.
	DialTimeout time.Duration `json:"dial_timeout"`

	TLSEnabled    bool `json:"tls_enabled"`
	TLSSkipVerify bool `json:"tls_skip_verify"`
}

// MessageID 包含单个消息的 ID 和大小。
type MessageID struct {
	// ID 是消息的数字索引（非唯一）。
	ID   int
	Size int
	// 仅当响应是对 UIDL 命令时才存在 UID。
	UID string
}

type MailInfo struct {
	// 邮件来自
	From string `json:"from"`
	// 收件时间
	Time int64 `json:"time"`
	// 邮件标题
	Title string `json:"title"`
	// 邮件内容
	Content string `json:"content"`
	// 邮件HTML格式内容
	HtmlContent string `json:"html_content"`
}

var (
	lineBreak   = []byte("\r\n")
	respOK      = []byte("+OK")   // `+OK` without additional info
	respOKInfo  = []byte("+OK ")  // `+OK <info>`
	respErr     = []byte("-ERR")  // `-ERR` without additional info
	respErrInfo = []byte("-ERR ") // `-ERR <info>`
)

// 使用现有连接返回一个新的客户端对象。
func NewPop3Client(opt Option) *Client {
	if opt.DialTimeout < time.Millisecond {
		opt.DialTimeout = time.Second * 3
	}
	return &Client{
		opt: opt,
	}
}

// NewConn 创建并返回实时 POP3 服务器连接。
func (c *Client) NewConn() (*Conn, error) {
	var (
		addr = fmt.Sprintf("%s:%d", c.opt.Host, c.opt.Port)
	)

	conn, err := net.DialTimeout("tcp", addr, c.opt.DialTimeout)
	if err != nil {
		return nil, err
	}

	if c.opt.TLSEnabled {
		// 跳过 TLS 主机验证。
		tlsCfg := tls.Config{}
		if c.opt.TLSSkipVerify {
			tlsCfg.InsecureSkipVerify = c.opt.TLSSkipVerify
		} else {
			tlsCfg.ServerName = c.opt.Host
		}

		conn = tls.Client(conn, &tlsCfg)
	}

	pCon := &Conn{
		conn: conn,
		r:    bufio.NewReader(conn),
		w:    bufio.NewWriter(conn),
	}

	// 通过问候语来验证连接。
	if _, err := pCon.ReadOne(); err != nil {
		return nil, err
	}
	return pCon, nil
}

// Send 向服务器发送一个 POP3 命令。给定的命令后缀为“\r\n”。
func (c *Conn) Send(b string) error {
	if _, err := c.w.WriteString(b + "\r\n"); err != nil {
		return err
	}
	return c.w.Flush()
}

// Cmd 向服务器发送命令。
// POP3 响应是单行或多行。
// 第一行总是带有 -ERR 以防出错或 +OK 以防操作成功。
// OK+ 后面总是跟在同一行上的响应，在单行响应的情况下，它是实际响应数据，或者在多行响应的情况下，后跟多行实际响应数据的帮助消息。
// 有关示例，请参见 https://www.shellhacks.com/retrieve-email-pop3-server-command-line。
func (c *Conn) Cmd(cmd string, isMulti bool, args ...interface{}) (*bytes.Buffer, error) {
	var cmdLine string
	if len(args) > 0 {
		format := " " + strings.TrimRight(strings.Repeat("%v ", len(args)), " ")
		cmdLine = fmt.Sprintf(cmd+format, args...)
	} else {
		cmdLine = cmd
	}
	if err := c.Send(cmdLine); err != nil {
		return nil, err
	}

	// 读取响应的第一行以获取 +OK -ERR 状态。
	b, err := c.ReadOne()
	if err != nil {
		return nil, err
	}

	// 单行响应。
	if !isMulti {
		return bytes.NewBuffer(b), err
	}

	buf, err := c.ReadAll()
	return buf, err
}

// ReadOne 从 conn 读取单行响应。
func (c *Conn) ReadOne() ([]byte, error) {
	b, _, err := c.r.ReadLine()
	if err != nil {
		return nil, err
	}

	r, err := parseResp(b)
	return r, err
}

// ReadAll 从连接中读取所有行，直到 POP3 多行终止符“.”遇到并返回所有读取行的 bytes.Buffer。
func (c *Conn) ReadAll() (*bytes.Buffer, error) {
	buf := &bytes.Buffer{}

	for {
		b, _, err := c.r.ReadLine()
		if err != nil {
			return nil, err
		}

		// "." 表示多行响应的结束。
		if bytes.Equal(b, []byte(".")) {
			break
		}

		if _, err := buf.Write(b); err != nil {
			return nil, err
		}
		if _, err := buf.Write(lineBreak); err != nil {
			return nil, err
		}
	}

	return buf, nil
}

// Auth 通过服务器验证给定的凭据。
func (c *Conn) Auth(user, password string) error {
	if err := c.User(user); err != nil {
		return err
	}

	if err := c.Pass(password); err != nil {
		return err
	}

	// 发出 NOOP 以强制服务器响应身份验证。
	return c.Noop()
}

// 用户将用户名发送到服务器。
func (c *Conn) User(s string) error {
	_, err := c.Cmd("USER", false, s)
	return err
}

// Pass 将密码发送到服务器。
func (c *Conn) Pass(s string) error {
	_, err := c.Cmd("PASS", false, s)
	return err
}

// Stat 返回收件箱中的消息数量及其总大小（以字节为单位）。
func (c *Conn) Stat() (int, int, error) {
	b, err := c.Cmd("STAT", false)
	if err != nil {
		return 0, 0, err
	}

	// 字节大小
	f := bytes.Fields(b.Bytes())

	// Total number of messages.
	count, err := strconv.Atoi(string(f[0]))
	if err != nil {
		return 0, 0, err
	}
	if count == 0 {
		return 0, 0, nil
	}

	// Total size of all messages in bytes.
	size, err := strconv.Atoi(string(f[1]))
	if err != nil {
		return 0, 0, err
	}

	return count, size, nil
}

// List 返回（消息 ID，消息大小）对的列表。
// 如果可选的 msgID > 0，则仅列出该特定消息。
// 消息 ID 是连续的，从 1 到 N。
func (c *Conn) List(msgID int) ([]MessageID, error) {
	var (
		buf *bytes.Buffer
		err error
	)

	if msgID <= 0 {
		// 列出所有消息的多行响应。
		buf, err = c.Cmd("LIST", true)
	} else {
		// 单行响应列出一条消息。
		buf, err = c.Cmd("LIST", false, msgID)
	}
	if err != nil {
		return nil, err
	}

	var (
		out   []MessageID
		lines = bytes.Split(buf.Bytes(), lineBreak)
	)

	for _, l := range lines {
		// id size
		f := bytes.Fields(l)
		if len(f) == 0 {
			break
		}

		id, err := strconv.Atoi(string(f[0]))
		if err != nil {
			return nil, err
		}

		size, err := strconv.Atoi(string(f[1]))
		if err != nil {
			return nil, err
		}

		out = append(out, MessageID{ID: id, Size: size})
	}

	return out, nil
}

// retr 通过给定的 msgid 下载消息，对其进行解析并将其作为 emersiongo-message.message.entity 对象返回。
func (c *Conn) Retr(msgID int) (*message.Entity, error) {
	b, err := c.Cmd("RETR", true, msgID)
	if err != nil {
		return nil, err
	}
	m, err := message.Read(b)
	if err != nil {
		if !message.IsUnknownCharset(err) {
			return nil, err
		}
	}

	return m, nil
}

// RetrRaw 通过给定的 msgID 下载消息并返回整个消息的原始 [] 字节。
func (c *Conn) RetrRaw(msgID int) (*bytes.Buffer, error) {
	b, err := c.Cmd("RETR", true, msgID)
	return b, err
}

// Top 通过其 ID 检索具有完整标题和正文的 numLines 行的消息。
func (c *Conn) Top(msgID int, numLines int) (*message.Entity, error) {
	b, err := c.Cmd("TOP", true, msgID, numLines)
	if err != nil {
		return nil, err
	}

	m, err := message.Read(b)
	if err != nil {
		return nil, err
	}

	return m, nil
}

// Dele 删除一条或多条消息。服务器仅在成功的 Quit() 后执行删除。
func (c *Conn) Dele(msgID ...int) error {
	for _, id := range msgID {
		_, err := c.Cmd("DELE", false, id)
		if err != nil {
			return err
		}
	}
	return nil
}

// Rset 清除当前会话中标记为删除的消息。
func (c *Conn) Rset() error {
	_, err := c.Cmd("RSET", false)
	return err
}

// Noop 向服务器发出一个什么都不做的 NOOP 命令。这对于延长打开的连接很有用。
func (c *Conn) Noop() error {
	_, err := c.Cmd("NOOP", false)
	return err
}

// Quit 向服务器发送 QUIT 命令并优雅地关闭连接。
// 消息删除（DELE 命令）仅由服务器在正常退出和关闭时执行。
func (c *Conn) Quit() error {
	if _, err := c.Cmd("QUIT", false); err != nil {
		return err
	}
	return c.conn.Close()
}

// parseResp 检查响应是否是以 `-ERR` 开头的错误，并返回错误指示符成功的消息。
// 对于成功的 `+OK` 消息，它返回剩余的响应字节。
func parseResp(b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if bytes.Equal(b, respOK) {
		return nil, nil
	} else if bytes.HasPrefix(b, respOKInfo) {
		return bytes.TrimPrefix(b, respOKInfo), nil
	} else if bytes.Equal(b, respErr) {
		return nil, errors.New("unknown error (no info specified in response)")
	} else if bytes.HasPrefix(b, respErrInfo) {
		return nil, errors.New(string(bytes.TrimPrefix(b, respErrInfo)))
	} else {
		return nil, fmt.Errorf("unknown response: %s. Neither -ERR, nor +OK", string(b))
	}
}

// 针对163邮箱，其他邮箱没有验证解析格式
func ParseMail(m *message.Entity) (*MailInfo, error) {
	received, err := m.Header.Text("Received")
	if err != nil {
		return nil, err
	}
	receiveds := strings.Split(received, ";")
	froms := strings.Split(receiveds[0], " ")
	date := strings.ReplaceAll(receiveds[1], "(CST)", "")
	tp, err := time.Parse(" Mon, 2 Jan 2006 15:04:05 -0700 ", date)
	if err != nil {
		return nil, err
	}
	text, _ := m.Header.Text("Subject")
	mailInfo := &MailInfo{
		From:  strings.ReplaceAll(froms[1], "$", "@"),
		Title: text,
		Time:  tp.Unix(),
	}
	if mr := m.MultipartReader(); mr != nil {
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			} else if err != nil {
				log.Fatal(err)
			}
			t, _, _ := p.Header.ContentType()

			b, err := io.ReadAll(p.Body)
			if t == "text/plain" {
				mailInfo.Content = string(b)
			}
			if t == "text/html" {
				mailInfo.HtmlContent = string(b)
			}
		}
	}
	return mailInfo, nil
}
