# feather-httpd example configuration

# Load all templates
template loaddir templates

route GET / {
    template respond home name "World" title "Home"
}

route GET /about {
    template respond about title "About"
}

route GET /hello {
    respond "Hello, World!"
}

route GET /hello/:name {
    header Content-Type text/plain
    respond "Hello, [param name]!"
}

route GET /api/users/:id {
    header Content-Type application/json
    respond [format {{"id": "%s", "name": "User %s"}} [param id] [param id]]
}

route POST /api/echo {
    header Content-Type application/json
    status 201
    respond [request body]
}

route GET /search {
    set q [query q ""]
    set limit [query limit 10]
    header Content-Type application/json
    respond [format {{"query": "%s", "limit": "%s"}} $q $limit]
}

route GET /info {
    header Content-Type text/plain
    respond "Method: [request method]\nPath: [request path]"
}

# ---- Chat Room Example (SSE) ----

template define chat {<!DOCTYPE html>
<html>
<head>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>Chat Room</title>
    <style>
        @import url('https://fonts.googleapis.com/css2?family=Space+Mono:wght@400;700&display=swap');
        * { box-sizing: border-box; margin: 0; padding: 0; }
        body { 
            font-family: 'Space Mono', monospace;
            background: #f5f5dc;
            color: #1a1a1a;
            min-height: 100vh;
            display: flex;
            flex-direction: column;
            padding: 2rem;
        }
        .container {
            max-width: 900px;
            margin: 0 auto;
            width: 100%;
            flex: 1;
            display: flex;
            flex-direction: column;
        }
        h1 { 
            font-size: 2.5rem;
            text-transform: uppercase;
            letter-spacing: 0.1em;
            border: 4px solid #1a1a1a;
            padding: 1rem;
            margin-bottom: 1rem;
            background: #ff6b6b;
            box-shadow: 8px 8px 0 #1a1a1a;
        }
        #messages { 
            flex: 1;
            min-height: 300px;
            overflow-y: auto; 
            border: 4px solid #1a1a1a;
            padding: 1rem;
            margin-bottom: 1rem;
            background: #fff;
            box-shadow: 8px 8px 0 #1a1a1a;
        }
        .msg { 
            margin: 0.75rem 0; 
            word-wrap: break-word;
            padding: 0.5rem;
            border-left: 4px solid #6c5ce7;
        }
        .msg .user { 
            color: #ff6b6b;
            font-weight: bold;
        }
        .msg .user::after { content: ":"; }
        .msg .system { 
            color: #6c5ce7;
            font-style: italic;
        }
        form { 
            display: flex; 
            gap: 0.5rem; 
            flex-wrap: wrap;
        }
        input { 
            font-family: inherit;
            font-size: 1rem;
            padding: 0.75rem;
            border: 4px solid #1a1a1a;
            background: #fff;
            color: #1a1a1a;
            outline: none;
        }
        input:focus {
            box-shadow: 4px 4px 0 #6c5ce7;
        }
        input[name=user] { width: 120px; }
        input[name=msg] { flex: 1; min-width: 150px; }
        input::placeholder { color: #888; }
        button { 
            font-family: inherit;
            font-size: 1rem;
            padding: 0.75rem 1.5rem;
            border: 4px solid #1a1a1a;
            background: #ffd93d;
            color: #1a1a1a;
            font-weight: bold;
            text-transform: uppercase;
            cursor: pointer;
            transition: all 0.1s;
        }
        button:hover {
            box-shadow: 4px 4px 0 #1a1a1a;
            transform: translate(-2px, -2px);
        }
        button:active {
            box-shadow: none;
            transform: translate(2px, 2px);
        }
        ::-webkit-scrollbar { width: 12px; }
        ::-webkit-scrollbar-track { background: #fff; border: 2px solid #1a1a1a; }
        ::-webkit-scrollbar-thumb { background: #1a1a1a; }
        @media (max-width: 600px) {
            body { padding: 1rem; }
            h1 { font-size: 1.5rem; padding: 0.75rem; }
            form { flex-direction: column; }
            input[name=user] { width: 100%; }
            button { width: 100%; }
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>Chat Room</h1>
        <div id="messages"></div>
        <form onsubmit="send(event)">
            <input name="user" placeholder="Name" value="anon">
            <input name="msg" placeholder="Message..." autofocus>
            <button>Send</button>
        </form>
    </div>
    <script>
        const messages = document.getElementById('messages');
        const es = new EventSource('/chat/stream?client=' + Math.random().toString(36).slice(2));
        
        es.addEventListener('message', (e) => {
            const {user, text} = JSON.parse(e.data);
            const div = document.createElement('div');
            div.className = 'msg';
            div.innerHTML = '<span class="user">' + user + '</span> ' + text;
            messages.appendChild(div);
            messages.scrollTop = messages.scrollHeight;
        });
        
        es.addEventListener('join', (e) => {
            const div = document.createElement('div');
            div.className = 'msg';
            div.innerHTML = '<span class="system">' + e.data + ' joined</span>';
            messages.appendChild(div);
        });
        
        es.addEventListener('leave', (e) => {
            const div = document.createElement('div');
            div.className = 'msg';
            div.innerHTML = '<span class="system">' + e.data + ' left</span>';
            messages.appendChild(div);
        });

        function send(e) {
            e.preventDefault();
            const form = e.target;
            const params = new URLSearchParams({user: form.user.value, text: form.msg.value});
            fetch('/chat/send?' + params, {method: 'POST'});
            form.msg.value = '';
        }
    </script>
</body>
</html>}

proc sse {conn event data} {
    respond -to $conn "event: $event\ndata: $data\n\n"
    flush -to $conn
}

proc on_chat_disconnect {client} {
    foreach conn [connections] {
        sse $conn leave $client
    }
}

route GET /chat {
    template respond chat
}

route GET /chat/stream {
    set client [query client]
    connection hold -as $client
    connection onclose $client on_chat_disconnect
    header Content-Type text/event-stream
    header Cache-Control no-cache
    sse $client join $client
    # Notify others
    foreach conn [connections] {
        if {$conn ne $client} {
            sse $conn join $client
        }
    }
}

route POST /chat/send {
    set msg [dict create user [query user] text [query text]]
    set json [json $msg -as {string user string text}]
    foreach conn [connections] {
        sse $conn message $json
    }
    respond "ok"
}

# Start server on port 8080
listen 8080
