# gaia-load-test

gaia压力测试工具:

举例: `./gatechain-load-test -home /Users/zhouhaw/.gaiacli -chain-id testnet -p 123456780 localhost:26657 gt11r6ld8avn676gl4cxz8h0v5memsjp8hncuj8vmr`

will output:

```
Stats          Avg       StdDev     Max      Total
Txs/sec        937      598       1998     9369
Blocks/sec     0.900     0.300      1        9
```


## 快速开始

### 使用二进制文件
进入gaia-load-test项目目录
```
go build
```


然后启动gaia

```
gaiad start
```
在gatechain-load-test目录，执行
```
./gaia-load-test -home /Users/zhouhaw/.gaiacli -chain-id testnet -p 123456780 localhost:26657 cosmos1r6ld8avn676gl4cxz8h0v5memsjp8hncuj8vmr
```

上述命令在不同窗口执行

## Usage

```
gaia压力测试工具

使用:
        ./gaia-load-test [-home dir] [-chain-id testnet] [-p password] [-r 2000] [endpoints] [address]

例子:
        ./gaia-load-test -home /Users/zhouhaw/.gaiacli -chain-id testing -p 123456780 -r 2000 localhost:26657 gt11r6ld8avn676gl4cxz8h0v5memsjp8hncuj8vmr
Flags:
  -home string
        测试链的配置文件路径
  -chain-id string
        测试链的chain-id
  -p string
        测试账户的密码(不能包含特殊符号)
  -endpoint string
        测试链暴露的tendermint端口，使用 `gated start` 启动gatechain即可
  -r int
        Txs per second to send in a connection (default 1500)
  -address string
       测试链转账账户地址，要保证其有充足的gt代币
```
