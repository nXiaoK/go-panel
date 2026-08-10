package result

import "time"

// R 统一响应结构，与 Java 版 com.admin.common.lang.R 保持一致
// {code:0 成功, msg, ts, data}
type R struct {
	Code int         `json:"code"`
	Msg  string      `json:"msg"`
	Ts   int64       `json:"ts"`
	Data interface{} `json:"data"`
}

func now() int64 { return time.Now().UnixMilli() }

// Ok 成功响应（带数据）
func Ok(data interface{}) R {
	return R{Code: 0, Msg: "操作成功", Ts: now(), Data: data}
}

// OkMsg 成功响应（消息作为 data，等价 Java R.ok(String)）
func OkMsg(msg string) R {
	return R{Code: 0, Msg: "操作成功", Ts: now(), Data: msg}
}

// OkEmpty 成功响应（无数据）
func OkEmpty() R {
	return R{Code: 0, Msg: "操作成功", Ts: now()}
}

// Err 业务错误响应 code=-1
func Err(msg string) R {
	return R{Code: -1, Msg: msg, Ts: now()}
}

// ErrCode 指定 code 的错误响应
func ErrCode(code int, msg string) R {
	return R{Code: code, Msg: msg, Ts: now()}
}
