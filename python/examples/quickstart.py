"""Create a sandbox, run a command, write/read a file, run Python, kill it."""

import sbbox

client = sbbox.Client()  # SBBOX_API_KEY from the environment

with client.sandboxes.create() as sb:
    print("sandbox", sb.id, "in", sb.region)
    print(sb.exec("uname -a").stdout.strip())

    sb.files.write("/home/user/hello.txt", "hello from the SDK\n")
    print(sb.files.read_text("/home/user/hello.txt").strip())

    r = sb.run_code("sum(range(10))")
    print("run_code ->", r.text)

    print("port 8000 would be served at", sb.port_url(8000))
