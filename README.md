# pop3客户端
GO语言实现的简单POP3客户端。


```
telnet pop.163.com 110
user 你的邮箱账号
pass 你的邮箱pop服务的密码（要申请）
list 列出你的邮件列表
retr id 根据id检出邮件内容
dele id 根据id删除邮件
noop 发送空消息保持心跳
quit 退出
```

## 使用
```go
// 初始化pop3客户端。
p := NewPop3Client(Option{
    Host:       "pop.163.com",
    Port:       995,
    TLSEnabled: true,
})

// 创建一个新的连接。
c, err := p.NewConn()
if err != nil {
    log.Fatal(err)
}
// POP3的连接是有状态的，一旦操作完成，应该以 Quit() 结束。
defer c.Quit()

// 认证用户名密码
if err := c.Auth(name, pass); err != nil {
    log.Fatal(err)
}

count, size, _ := c.Stat()
fmt.Println("共有=", count, "条邮件，大小=", size)

// 拉取所有消息 ID 及其大小的列表。
msgs, _ := c.List(0)
for _, m := range msgs {
    fmt.Println("id=", m.ID, "size=", m.Size)
}

// 获取所有邮件，并倒序获取最新邮件
for id := count; id > 0; id-- {
    m, _ := c.Retr(id)
    mail, err := ParseMail(m)
    if err != nil {
        continue
    }
    marshal, _ := json.Marshal(mail)
    fmt.Println(string(marshal))
}

// 删除所有消息。服务器仅在成功 Quit() 后执行删除
for id := 1; id <= count; id++ {
    // 谨慎操作删除
    c.Dele(id)
}
```