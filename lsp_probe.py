import json, subprocess, sys, os

def msg(obj):
    body = json.dumps(obj).encode()
    return b"Content-Length: %d\r\n\r\n%s" % (len(body), body)

src = 'def f(x)\n  x\nend\nputs f [1], [2]\na = [1]\nputs a [0]\nnope [1] { |v| v }\n'
p = subprocess.Popen([sys.argv[1], "lsp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
out = []
p.stdin.write(msg({"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}))
p.stdin.write(msg({"jsonrpc":"2.0","method":"initialized","params":{}}))
p.stdin.write(msg({"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///probe.vibe","languageId":"vibescript","version":1,"text":src}}}))
p.stdin.write(msg({"jsonrpc":"2.0","id":2,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"file:///probe.vibe"}}}))
p.stdin.write(msg({"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///probe.vibe"},"position":{"line":3,"character":5}}}))
p.stdin.write(msg({"jsonrpc":"2.0","id":9,"method":"shutdown","params":None}))
p.stdin.write(msg({"jsonrpc":"2.0","method":"exit"}))
p.stdin.flush(); p.stdin.close()
data = p.stdout.read()
p.wait(timeout=10)
# parse LSP frames
i = 0
while True:
    j = data.find(b"\r\n\r\n", i)
    if j < 0: break
    hdr = data[i:j].decode()
    ln = int([l.split(":")[1] for l in hdr.split("\r\n") if l.lower().startswith("content-length")][0])
    body = data[j+4:j+4+ln]
    o = json.loads(body)
    key = o.get("method") or ("resp:%s" % o.get("id"))
    print(key, json.dumps(o.get("params") or o.get("result"))[:300])
    i = j+4+ln
print("STDERR:", p.stderr.read().decode()[:300])
