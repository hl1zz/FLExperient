import os
import time
import random
import sys
from flask import Flask, request, jsonify
from threading import Thread, Event
import requests

role = os.getenv("ROLE")
rounds = int(os.getenv("ROUNDS", 3))

if role == "master":
    # ========== Master 逻辑 ==========
    worker_count = int(os.getenv("WORKER_COUNT", 3))

    app = Flask(__name__)

    # 已注册的 Worker 信息: { rank: address }
    registered_workers = {}
    all_workers_registered = Event()

    # 每轮收到的梯度
    received_gradients = {}
    round_complete_events = {}

    for r_num in range(rounds):
        round_complete_events[r_num] = Event()

    @app.route("/register", methods=["POST"])
    def register():
        """Worker 启动后调用，注册自己"""
        data = request.json
        rank = data["rank"]
        address = data["address"]

        registered_workers[rank] = address
        print(f"[Master] Worker-{rank} registered at {address} ({len(registered_workers)}/{worker_count})")

        if len(registered_workers) >= worker_count:
            print(f"[Master] ✅ All {worker_count} workers registered!")
            all_workers_registered.set()

        return jsonify({"status": "ok"})

    @app.route("/upload", methods=["POST"])
    def upload():
        """Worker 训练完后上传梯度"""
        data = request.json
        round_num = data["round"]
        rank = data["rank"]
        weights = data["weights"]

        if round_num not in received_gradients:
            received_gradients[round_num] = []

        received_gradients[round_num].append({
            "rank": rank,
            "weights": weights
        })

        print(f"[Master] Received gradients from Worker-{rank} (Round {round_num})")
        print(f"[Master] Progress: {len(received_gradients[round_num])}/{worker_count}")

        if len(received_gradients[round_num]) >= worker_count:
            print(f"[Master] ✅ All gradients received for Round {round_num}")
            round_complete_events[round_num].set()

        return jsonify({"status": "ok"})

    @app.route("/health", methods=["GET"])
    def health():
        return jsonify({
            "status": "healthy",
            "registered_workers": len(registered_workers),
            "total_workers": worker_count
        })

    @app.route("/status", methods=["GET"])
    def status():
        return jsonify({
            "registered_workers": len(registered_workers),
            "total_workers": worker_count,
            "total_rounds": rounds
        })

    def aggregate(models):
        """聚合所有 Worker 的权重"""
        avg_weights = sum(m["weights"][0] for m in models) / len(models)
        return {"aggregated_weight": round(avg_weights, 4)}

    def send_train(worker_addr, round_num, global_params):
        """给单个 Worker 发送训练指令"""
        try:
            url = f"http://{worker_addr}/train"
            data = {"round": round_num, "global_params": global_params}
            resp = requests.post(url, json=data, timeout=30)
            if resp.status_code == 200:
                print(f"[Master] ✅ Sent /train to {worker_addr} (Round {round_num})")
            else:
                print(f"[Master] ❌ Failed to send /train to {worker_addr}: {resp.status_code}")
        except Exception as e:
            print(f"[Master] ❌ Error sending /train to {worker_addr}: {e}")

    def send_shutdown(worker_addr):
        """给单个 Worker 发送关闭指令"""
        try:
            url = f"http://{worker_addr}/shutdown"
            requests.post(url, json={}, timeout=10)
            print(f"[Master] Sent /shutdown to {worker_addr}")
        except Exception as e:
            print(f"[Master] Warning: shutdown to {worker_addr} failed: {e}")

    def training_loop():
        """Master 的主训练循环（在后台线程运行）"""
        print(f"[Master] Waiting for all {worker_count} workers to register...")
        all_workers_registered.wait()

        print(f"[Master] Starting federated learning for {rounds} rounds")
        global_params = {"aggregated_weight": random.uniform(0.1, 0.9)}

        for round_num in range(rounds):
            print(f"\n[Master] ━━━━━━━━━━ Round {round_num} ━━━━━━━━━━")

            # 步骤 1: 给所有 Worker 发送 /train（并行）
            print(f"[Master] Sending /train to all workers with global params: {global_params}")
            threads = []
            for rank, addr in registered_workers.items():
                t = Thread(target=send_train, args=(addr, round_num, global_params))
                t.start()
                threads.append(t)
            for t in threads:
                t.join()

            # 步骤 2: 等待所有 Worker 上传梯度
            print(f"[Master] Waiting for all workers to upload gradients...")
            if not round_complete_events[round_num].wait(timeout=300):
                print(f"[Master] ❌ Timeout waiting for gradients in Round {round_num}")
                break

            # 步骤 3: 聚合
            global_params = aggregate(received_gradients[round_num])
            print(f"[Master] ✅ Round {round_num} aggregated: {global_params}")

        # 所有轮次完成，通知 Worker 关闭
        print(f"\n[Master] 🎉 All {rounds} rounds completed!")
        print(f"[Master] Sending /shutdown to all workers...")

        for rank, addr in registered_workers.items():
            send_shutdown(addr)

        print(f"[Master] Shutting down in 5 seconds...")
        time.sleep(5)
        os._exit(0)

    # 启动训练循环线程
    training_thread = Thread(target=training_loop, daemon=True)
    training_thread.start()

    print(f"[Master] Starting server, expecting {worker_count} workers for {rounds} rounds")
    app.run(host="0.0.0.0", port=8080)

elif role == "worker":
    # ========== Worker 逻辑 ==========
    rank = int(os.getenv("RANK"))
    master_addr = os.getenv("MASTER_ADDR")
    pod_ip = os.getenv("POD_IP", "127.0.0.1")

    app = Flask(__name__)

    # 训练相关状态
    train_event = Event()
    shutdown_event = Event()
    current_train_data = None

    @app.route("/train", methods=["POST"])
    def train():
        """Master 调用，传入全局参数，触发本地训练"""
        global current_train_data
        data = request.json
        round_num = data["round"]
        global_params = data["global_params"]

        print(f"[Worker-{rank}] 📥 Received /train for Round {round_num}, params: {global_params}")
        current_train_data = {"round": round_num, "global_params": global_params}
        train_event.set()

        return jsonify({"status": "ok", "rank": rank})

    @app.route("/shutdown", methods=["POST"])
    def shutdown():
        """Master 调用，通知 Worker 退出"""
        print(f"[Worker-{rank}] 📥 Received /shutdown")
        shutdown_event.set()
        return jsonify({"status": "ok"})

    @app.route("/health", methods=["GET"])
    def health():
        return jsonify({"status": "healthy", "rank": rank})

    def register_to_master():
        """向 Master 注册自己"""
        my_address = f"{pod_ip}:8081"
        max_retries = 30

        for attempt in range(max_retries):
            try:
                resp = requests.post(
                    f"http://{master_addr}/register",
                    json={"rank": rank, "address": my_address},
                    timeout=10
                )
                if resp.status_code == 200:
                    print(f"[Worker-{rank}] ✅ Registered to Master at {master_addr} (my addr: {my_address})")
                    return True
            except Exception as e:
                print(f"[Worker-{rank}] Waiting for Master... attempt {attempt + 1}/{max_retries}: {e}")

            time.sleep(2)

        print(f"[Worker-{rank}] ❌ Failed to register to Master after {max_retries} attempts")
        return False

    def do_local_training(round_num, global_params):
        """执行本地训练（模拟）"""
        print(f"[Worker-{rank}] 🏋️ Training Round {round_num}...")

        if global_params and "aggregated_weight" in global_params:
            base_weight = global_params["aggregated_weight"]
            local_weight = base_weight + random.uniform(-0.1, 0.1)
        else:
            local_weight = random.uniform(0.1, 0.9)

        time.sleep(2)  # 模拟训练耗时
        print(f"[Worker-{rank}] ✅ Training done, local_weight={local_weight:.4f}")
        return local_weight

    def upload_gradients(round_num, local_weight):
        """上传梯度到 Master"""
        try:
            resp = requests.post(
                f"http://{master_addr}/upload",
                json={"rank": rank, "round": round_num, "weights": [local_weight]},
                timeout=10
            )
            print(f"[Worker-{rank}] ✅ Uploaded gradients to Master")
        except Exception as e:
            print(f"[Worker-{rank}] ❌ Upload error: {e}")

    def worker_loop():
        """Worker 的主循环：等待 Master 指令 → 训练 → 上传"""
        # 先注册到 Master
        if not register_to_master():
            print(f"[Worker-{rank}] Exiting due to registration failure")
            os._exit(1)

        # 循环等待 Master 的 /train 指令
        while not shutdown_event.is_set():
            print(f"[Worker-{rank}] Waiting for /train command from Master...")
            train_event.wait(timeout=300)

            if shutdown_event.is_set():
                break

            if train_event.is_set():
                train_event.clear()
                round_num = current_train_data["round"]
                global_params = current_train_data["global_params"]

                # 本地训练
                local_weight = do_local_training(round_num, global_params)

                # 上传梯度
                upload_gradients(round_num, local_weight)

        print(f"[Worker-{rank}] 👋 Shutting down gracefully")
        time.sleep(2)
        os._exit(0)

    # 启动 Worker 主循环（后台线程）
    worker_thread = Thread(target=worker_loop, daemon=True)
    worker_thread.start()

    print(f"[Worker-{rank}] Starting Flask server on port 8081")
    app.run(host="0.0.0.0", port=8081, debug=False, use_reloader=False)

else:
    print(f"Unknown role: {role}")
    sys.exit(1)