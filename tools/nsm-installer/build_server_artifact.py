#!/usr/bin/env python3
"""Build and patch the ready-to-run NSM Dedicated Server artifact (NSM-Server.zip)
from NSM-server-v0.8.1-localtest.zip + cfx-server-data resources.
"""
import hashlib
import io
import json
import re
import struct
import sys
import zipfile
from datetime import datetime, timezone
from pathlib import Path


def sha(b: bytes) -> str:
    return hashlib.sha256(b).hexdigest()


def parse_pe_sections(data: bytearray):
    if len(data) < 0x100 or data[:2] != b"MZ":
        return []
    pe_off = struct.unpack_from("<I", data, 0x3C)[0]
    if pe_off + 24 > len(data) or data[pe_off : pe_off + 4] != b"PE\x00\x00":
        return []
    num_sec = struct.unpack_from("<H", data, pe_off + 6)[0]
    opt_size = struct.unpack_from("<H", data, pe_off + 20)[0]
    sec_table = pe_off + 24 + opt_size
    sections = []
    for i in range(num_sec):
        s_off = sec_table + i * 40
        if s_off + 40 > len(data):
            break
        s_name = data[s_off : s_off + 8].rstrip(b"\x00").decode("ascii", "ignore")
        vsize, rva, raw_size, raw_ptr = struct.unpack_from("<IIII", data, s_off + 8)
        sections.append([s_off, s_name, vsize, rva, raw_size, raw_ptr])
    return sections


def file_off_to_rva(sections, foff: int):
    for _, _, vsize, rva, raw_size, raw_ptr in sections:
        if raw_ptr <= foff < raw_ptr + raw_size:
            return rva + (foff - raw_ptr)
    return None


def alloc_in_rdata_cave(data: bytearray, sections, payload: bytes):
    for sec in sections:
        s_off, s_name, vsize, rva, raw_size, raw_ptr = sec
        if s_name in (".rdata", ".data") and vsize + len(payload) + 16 <= raw_size:
            cave_off = (vsize + 15) & ~15
            if cave_off + len(payload) <= raw_size:
                cave_raw = raw_ptr + cave_off
                new_rva = rva + cave_off
                data[cave_raw : cave_raw + len(payload)] = payload
                sec[2] = cave_off + len(payload)
                struct.pack_into("<I", data, s_off + 8, sec[2])
                return new_rva
    return None


def patch_nearby_ascii_lengths(data: bytearray, lea_pos: int, sec_start: int, sec_end: int, old_len: int, new_len: int) -> int:
    if old_len == new_len or old_len <= 4:
        return 0
    old_u32 = struct.pack("<I", old_len)
    new_u32 = struct.pack("<I", new_len)
    win_start = max(sec_start, lea_pos - 32)
    win_end = min(sec_end - 8, lea_pos + 40)
    patched = 0
    k = win_start
    while k < win_end:
        # mov r8d..r15d, imm32 (41 B8..BF imm32) or mov rax..rdi, imm32 (48/49 C7 C0..C7 imm32)
        if data[k] == 0x41 and 0xB8 <= data[k + 1] <= 0xBF and data[k + 2 : k + 6] == old_u32:
            data[k + 2 : k + 6] = new_u32
            patched += 1
            k += 6
            continue
        if 0xB8 <= data[k] <= 0xBF and data[k + 1 : k + 5] == old_u32:
            data[k + 1 : k + 5] = new_u32
            patched += 1
            k += 5
            continue
        if data[k] in (0x48, 0x49) and data[k + 1] == 0xC7 and 0xC0 <= data[k + 2] <= 0xC7 and data[k + 3 : k + 7] == old_u32:
            data[k + 3 : k + 7] = new_u32
            patched += 1
            k += 7
            continue
        if data[k] in (0x48, 0x49) and data[k + 1] == 0xC7 and data[k + 2] == 0x44 and data[k + 3] == 0x24 and data[k + 5 : k + 9] == old_u32:
            data[k + 5 : k + 9] = new_u32
            patched += 1
            k += 9
            continue
        if data[k] == 0xC7 and data[k + 1] == 0x44 and data[k + 2] == 0x24 and data[k + 4 : k + 8] == old_u32:
            data[k + 4 : k + 8] = new_u32
            patched += 1
            k += 8
            continue
        # cmp qword ptr [reg + disp8], imm8 (48/49 83 78..7F disp8 imm8) or [rsp + disp8] (48 83 7C 24 disp8 imm8)
        if old_len < 128 and new_len < 128:
            if data[k] in (0x48, 0x49) and data[k + 1] == 0x83 and 0x78 <= data[k + 2] <= 0x7F and data[k + 2] != 0x7C and data[k + 4] == old_len:
                data[k + 4] = new_len
                patched += 1
                k += 5
                continue
            if data[k] in (0x48, 0x49) and data[k + 1] == 0x83 and data[k + 2] == 0x7C and data[k + 3] == 0x24 and data[k + 5] == old_len:
                data[k + 5] = new_len
                patched += 1
                k += 6
                continue
            if data[k] in (0x48, 0x49) and data[k + 1] == 0x83 and 0xF8 <= data[k + 2] <= 0xFF and data[k + 3] == old_len:
                data[k + 3] = new_len
                patched += 1
                k += 4
                continue
        k += 1
    return patched


def redirect_ascii_string(data: bytearray, sections, old_bytes: bytes, new_bytes: bytes, label: str) -> int:
    """Replace old_bytes with new_bytes in-place when len(new_bytes) <= len(old_bytes),
    or allocate new_bytes+NUL in the .rdata cave and redirect all RIP-relative LEA instructions
    plus nearby compile-time length immediates.
    """
    needle = old_bytes + b"\x00"
    patched = 0
    pos = 0
    while True:
        idx = data.find(needle, pos)
        if idx == -1:
            break
        pos = idx + 1
        old_rva = file_off_to_rva(sections, idx)
        if len(new_bytes) <= len(old_bytes):
            if old_rva is not None and len(new_bytes) != len(old_bytes):
                for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                    if t_name in (".text", ".zdata", ".ctext"):
                        t_end = t_raw_ptr + min(t_vsize, t_raw_size)
                        for p in range(t_raw_ptr, t_end - 7):
                            if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p + 3)[0]
                                if (t_rva + (p - t_raw_ptr) + 7 + disp) == old_rva:
                                    patch_nearby_ascii_lengths(data, p, t_raw_ptr, t_end, len(old_bytes), len(new_bytes))
            data[idx : idx + len(old_bytes) + 1] = (new_bytes + b"\x00").ljust(len(old_bytes) + 1, b"\x00")
            patched += 1
            print(f"[server-pe] {label}: in-place {old_bytes!r} -> {new_bytes!r} at 0x{idx:x}")
        elif old_rva is not None:
            new_rva = alloc_in_rdata_cave(data, sections, new_bytes + b"\x00")
            if new_rva is not None:
                lea_count = 0
                len_count = 0
                for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                    if t_name in (".text", ".zdata", ".ctext"):
                        t_end = t_raw_ptr + min(t_vsize, t_raw_size)
                        for p in range(t_raw_ptr, t_end - 7):
                            if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p + 3)[0]
                                insn_end_rva = t_rva + (p - t_raw_ptr) + 7
                                if insn_end_rva + disp == old_rva:
                                    struct.pack_into("<i", data, p + 3, new_rva - insn_end_rva)
                                    lea_count += 1
                                    len_count += patch_nearby_ascii_lengths(
                                        data, p, t_raw_ptr, t_end, len(old_bytes), len(new_bytes)
                                    )
                            elif data[p] == 0x8D and (data[p + 1] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p + 2)[0]
                                insn_end_rva = t_rva + (p - t_raw_ptr) + 6
                                if insn_end_rva + disp == old_rva:
                                    struct.pack_into("<i", data, p + 2, new_rva - insn_end_rva)
                                    lea_count += 1
                                    len_count += patch_nearby_ascii_lengths(
                                        data, p, t_raw_ptr, t_end, len(old_bytes), len(new_bytes)
                                    )
                # Overwrite old string so static scans verify no stale URL remains
                data[idx : idx + len(needle)] = b"\x00" * len(needle)
                patched += 1
                print(
                    f"[server-pe] {label}: cave-redirected {old_bytes!r} -> {new_bytes!r} "
                    f"(0x{old_rva:x} -> 0x{new_rva:x}, {lea_count} LEAs, {len_count} len immediates)"
                )
    return patched


def patch_server_pe_binary(raw_bytes: bytes, filename: str) -> bytes:
    data = bytearray(raw_bytes)
    orig_len = len(data)
    sections = parse_pe_sections(data)
    if not sections:
        return raw_bytes

    # 1. Masterlist Heartbeat URL (kDefaultServerList) -> http://147.189.174.173/api/serverlist/ingress
    for old_hb in (
        b"http://127.0.0.1/api/serverlist/ingress",
        b"http://127.0.0.1:80/api/serverlist/ingress",
        b"http://194.104.146.133/api/serverlist/ingress",
        b"https://servers-frontend.fivem.net/api/serverlist/ingress",
    ):
        redirect_ascii_string(
            data,
            sections,
            old_hb,
            b"http://147.189.174.173/api/serverlist/ingress",
            filename,
        )

    # 2. Ticket RSA Public Key URL (CNL_ENDPOINT "api/ticket/pubkey") -> https://lambda.fivem.net/api/ticket/pubkey
    for old_pk in (
        b"http://194.104.146.133/api/ticket/pubkey",
        b"http://127.0.0.1/api/ticket/pubkey",
        b"http://127.0.0.1:80/api/ticket/pubkey",
        b"http://147.189.174.173/api/ticket/pubkey",
    ):
        redirect_ascii_string(
            data,
            sections,
            old_pk,
            b"https://lambda.fivem.net/api/ticket/pubkey",
            filename,
        )

    # 3. Pool Size Limits URLs (limitsFileUrl) -> http://147.189.174.173/v1/public/pool-size-limits/...
    for old_pool, new_pool in (
        (
            b"http://194.104.146.133/v1/public/pool-size-limits/fivem",
            b"http://147.189.174.173/v1/public/pool-size-limits/fivem",
        ),
        (
            b"http://194.104.146.133/v1/public/pool-size-limits/redm",
            b"http://147.189.174.173/v1/public/pool-size-limits/redm",
        ),
        (
            b"http://194.104.146.133/v1/public/pool-size-limits/",
            b"http://147.189.174.173/v1/public/pool-size-limits/",
        ),
        (
            b"http://127.0.0.1/v1/public/pool-size-limits/fivem",
            b"http://147.189.174.173/v1/public/pool-size-limits/fivem",
        ),
        (
            b"http://127.0.0.1/v1/public/pool-size-limits/redm",
            b"http://147.189.174.173/v1/public/pool-size-limits/redm",
        ),
        (
            b"http://127.0.0.1/v1/public/pool-size-limits/",
            b"http://147.189.174.173/v1/public/pool-size-limits/",
        ),
    ):
        redirect_ascii_string(data, sections, old_pool, new_pool, filename)

    # 4. Remaining 194.104.146.133 & Discord references -> 147.189.174.173 / nsmultiplayer.com
    data[:] = data.replace(b"http://194.104.146.133", b"http://147.189.174.173")
    data[:] = data.replace(
        b"https://discord.gg/UXj5QfCX88",
        b"https://nsmultiplayer.com\x00\x00\x00\x00",
    )

    # 5. citizen-server-impl.dll specific fixes:
    #    a) SteamIdentityProvider: STEAM_APPID_FIVEM (2676230 -> 218 Source SDK Base 2007)
    #    b) InitConnectMethod: allow empty/unset sv_serverId when svadhesive.dll is not loaded
    if filename.lower().endswith("citizen-server-impl.dll"):
        fivem_appid = struct.pack("<I", 2676230)
        redm_appid = struct.pack("<I", 4333400)
        nsm_appid = struct.pack("<I", 218)
        for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
            if t_name in (".text", ".zdata", ".ctext"):
                t_end = t_raw_ptr + min(t_vsize, t_raw_size)
                pos = t_raw_ptr
                while True:
                    idx = data.find(fivem_appid, pos, t_end)
                    if idx == -1:
                        break
                    pos = idx + 1
                    window = data[max(t_raw_ptr, idx - 64) : min(t_end, idx + 64)]
                    if redm_appid in window:
                        data[idx : idx + 4] = nsm_appid
                        print(f"[server-pe] {filename}: patched STEAM_APPID_FIVEM 2676230 -> 218 at 0x{idx:x}")

        # Locate "CitizenFX ticket authorization failed. (4)" and bypass the missing sv_serverId check
        err4_str = b"CitizenFX ticket authorization failed. (4)\x00"
        err4_off = data.find(err4_str)
        if err4_off != -1:
            err4_rva = file_off_to_rva(sections, err4_off)
            if err4_rva is not None:
                for _, t_name, t_vsize, t_rva, t_raw_size, t_raw_ptr in sections:
                    if t_name in (".text", ".zdata", ".ctext"):
                        end = t_raw_ptr + min(t_vsize, t_raw_size) - 7
                        for p in range(t_raw_ptr, end):
                            if data[p] in (0x48, 0x4C) and data[p + 1] == 0x8D and (data[p + 2] & 0xC7) == 0x05:
                                disp = struct.unpack_from("<i", data, p + 3)[0]
                                insn_end_rva = t_rva + (p - t_raw_ptr) + 7
                                if insn_end_rva + disp == err4_rva:
                                    # Inspect preceding 32 bytes for jnz (skip error4) or jz (jump to error4)
                                    patched_branch = False
                                    for bpos in range(p - 1, max(t_raw_ptr, p - 32), -1):
                                        if data[bpos] == 0x75:  # jnz rel8
                                            rel8 = struct.unpack_from("<b", data, bpos + 1)[0]
                                            target = bpos + 2 + rel8
                                            if target > p + 7:
                                                data[bpos] = 0xEB  # jmp rel8
                                                patched_branch = True
                                                print(
                                                    f"[server-pe] {filename}: patched serverIdMatch jnz->jmp at 0x{bpos:x}"
                                                )
                                                break
                                        elif data[bpos] == 0x0F and data[bpos + 1] == 0x85:  # jnz rel32
                                            rel32 = struct.unpack_from("<i", data, bpos + 2)[0]
                                            target = bpos + 6 + rel32
                                            if target > p + 7:
                                                data[bpos : bpos + 2] = b"\x90\xe9"
                                                patched_branch = True
                                                print(
                                                    f"[server-pe] {filename}: patched serverIdMatch jnz->jmp32 at 0x{bpos:x}"
                                                )
                                                break
                                        elif data[bpos] == 0x74:  # jz rel8 into error4 block
                                            rel8 = struct.unpack_from("<b", data, bpos + 1)[0]
                                            target = bpos + 2 + rel8
                                            if p - 12 <= target <= p:
                                                data[bpos : bpos + 2] = b"\x90\x90"
                                                patched_branch = True
                                                print(
                                                    f"[server-pe] {filename}: NOPed serverIdMatch jz at 0x{bpos:x}"
                                                )
                                                break
                                        elif data[bpos] == 0x0F and data[bpos + 1] == 0x84:  # jz rel32 into error4 block
                                            rel32 = struct.unpack_from("<i", data, bpos + 2)[0]
                                            target = bpos + 6 + rel32
                                            if p - 12 <= target <= p:
                                                data[bpos : bpos + 6] = b"\x90" * 6
                                                patched_branch = True
                                                print(
                                                    f"[server-pe] {filename}: NOPed serverIdMatch jz32 at 0x{bpos:x}"
                                                )
                                                break

    assert len(data) == orig_len
    return bytes(data)


DEFAULT_SERVER_CFG = """# ============================================================================
# NSM Dedicated Server Configuration (server.cfg) — Powered by AFX
# ============================================================================
# Only change the IP if you're using a server with multiple network interfaces,
# otherwise change the port only.
endpoint_add_tcp "0.0.0.0:30120"
endpoint_add_udp "0.0.0.0:30120"

# ---- NSM Masterlist & Keymaster Registration ----
# 1. Visit https://nsmultiplayer.com/keymaster and sign in to your AFX account.
# 2. Click "Generate Server Key" and paste your nsm_srv_... key below:
sets sv_nsmServerKey ""

# Live NSM Masterlist endpoint (automatically lists your server in NSM.exe & https://nsmultiplayer.com/servers)
sv_master1 "http://147.189.174.173/api/serverlist/ingress"
set sv_serverId ""

# ---- Standard Core Resources ----
ensure mapmanager
ensure chat
ensure spawnmanager
ensure sessionmanager
ensure basic-gamemode
ensure hardcap
ensure rconlog

# ---- Server Identity & Tags ----
sv_hostname "^5NSM Community Server ^0| ^2Powered by AFX"
sets sv_projectName "NSM Community Server"
sets sv_projectDesc "Official NSM Multiplayer Dedicated Server — Powered by AFX"
sets tags "default,nsm,afx,roleplay,freeroam"
sets locale "en-US"

# ---- Gameplay & OneSync Settings ----
sv_scriptHookAllowed 0
set onesync on
sv_maxclients 48

# ---- Steam Web API Key (Optional) ----
# If you want to use Steam identifiers (steam:...), get a key from https://steamcommunity.com/dev/apikey
set steam_webApiKey "none"

# ---- System Admins ----
add_ace group.admin command allow
add_ace group.admin command.quit deny
add_principal identifier.nsm:1 group.admin
"""

START_SERVER_BAT = """@echo off
title NSM Dedicated Server - Powered by AFX
cd /d "%~dp0"
echo ============================================================================
echo   NSM Dedicated Server - Powered by AFX
echo   Masterlist: https://nsmultiplayer.com
echo   Keymaster:  https://nsmultiplayer.com/keymaster
echo ============================================================================
echo.
if not exist "server.cfg" (
    echo [ERROR] server.cfg not found in %CD%
    pause
    exit /b 1
)
nsm-server.exe +exec server.cfg
pause
"""

README_SERVER_TXT = """============================================================================
NSM Dedicated Server Artifact (NSM-Server.zip) — Powered by AFX
============================================================================

Quick Start (Windows):
1. Extract this archive into a folder (e.g. C:\\NSM-Server).
2. Open https://nsmultiplayer.com/keymaster in your browser and generate a
   Server Registration Key (nsm_srv_...).
3. Open server.cfg in any text editor and paste your key into:
     sets sv_nsmServerKey "nsm_srv_..."
   and customize your sv_hostname / sv_projectName / sv_projectDesc.
4. Double-click START-SERVER.bat (or run: nsm-server.exe +exec server.cfg).
5. Make sure port 30120 (TCP & UDP) is open in Windows Firewall / router.
   Your server will automatically appear in NSM.exe and on:
     https://nsmultiplayer.com/servers
"""


def build_server_zip(base_server_zip: Path, cfx_server_data_dir: Path, out_zip: Path) -> None:
    now_utc = datetime.now(timezone.utc)
    build_dt = (2028, now_utc.month, now_utc.day, now_utc.hour, now_utc.minute, now_utc.second)

    with zipfile.ZipFile(base_server_zip, "r") as zin, zipfile.ZipFile(
        out_zip, "w", zipfile.ZIP_DEFLATED, compresslevel=6
    ) as zout:
        raw_names = [i.filename for i in zin.infolist() if not i.is_dir()]
        # Determine if base_server_zip has a top-level folder prefix
        first_parts = {n.split("/", 1)[0] for n in raw_names if "/" in n}
        top_prefix = ""
        if len(first_parts) == 1 and all(n.startswith(next(iter(first_parts)) + "/") for n in raw_names):
            top_prefix = next(iter(first_parts)) + "/"

        has_svadhesive_dll = any(n.lower().endswith("svadhesive.dll") for n in raw_names)
        written = set()

        for info in zin.infolist():
            if info.is_dir():
                continue
            rel = info.filename[len(top_prefix) :] if top_prefix and info.filename.startswith(top_prefix) else info.filename
            rel = rel.lstrip("/")
            if not rel:
                continue
            low = rel.lower()
            if low in ("server.cfg", "start-server.bat", "readme-server.txt"):
                continue

            data = zin.read(info.filename)
            if low == "components.json" and not has_svadhesive_dll:
                try:
                    comp_list = json.loads(data.decode("utf-8"))
                    comp_list = [c for c in comp_list if c != "svadhesive"]
                    data = (json.dumps(comp_list, indent=2) + "\n").encode("utf-8")
                    print(f"[server-pack] stripped svadhesive from components.json ({len(comp_list)} components)")
                except Exception as e:
                    print(f"[server-pack] warning parsing components.json: {e}")
            elif low.endswith((".exe", ".dll")):
                data = patch_server_pe_binary(data, rel)

            out_name = "NSM-Server/" + rel
            zinfo = zipfile.ZipInfo(filename=out_name, date_time=build_dt)
            zinfo.compress_type = zipfile.ZIP_DEFLATED
            zout.writestr(zinfo, data)
            written.add(out_name)

        # Bundle standard cfx-server-data resources into NSM-Server/resources/
        res_root = cfx_server_data_dir / "resources"
        if res_root.is_dir():
            for p in sorted(res_root.rglob("*")):
                if p.is_file() and ".git" not in p.parts:
                    rel_res = p.relative_to(cfx_server_data_dir).as_posix()
                    out_name = "NSM-Server/" + rel_res
                    if out_name not in written:
                        zinfo = zipfile.ZipInfo(filename=out_name, date_time=build_dt)
                        zinfo.compress_type = zipfile.ZIP_DEFLATED
                        zout.writestr(zinfo, p.read_bytes())
                        written.add(out_name)

        for extra_name, extra_text in (
            ("NSM-Server/server.cfg", DEFAULT_SERVER_CFG),
            ("NSM-Server/START-SERVER.bat", START_SERVER_BAT),
            ("NSM-Server/README-SERVER.txt", README_SERVER_TXT),
        ):
            zinfo = zipfile.ZipInfo(filename=extra_name, date_time=build_dt)
            zinfo.compress_type = zipfile.ZIP_DEFLATED
            zout.writestr(zinfo, extra_text.replace("\r\n", "\n").replace("\n", "\r\n").encode("utf-8"))
            written.add(extra_name)

    # Verify the resulting NSM-Server.zip
    with zipfile.ZipFile(out_zip, "r") as zcheck:
        assert zcheck.testzip() is None
        names = set(zcheck.namelist())
        assert "NSM-Server/nsm-server.exe" in names
        assert "NSM-Server/citizen-server-impl.dll" in names
        assert "NSM-Server/components.json" in names
        assert "NSM-Server/server.cfg" in names
        assert "NSM-Server/START-SERVER.bat" in names
        assert any(n.startswith("NSM-Server/resources/") for n in names)

        comps = json.loads(zcheck.read("NSM-Server/components.json").decode("utf-8"))
        assert "svadhesive" not in comps

        impl_bytes = zcheck.read("NSM-Server/citizen-server-impl.dll")
        assert b"http://147.189.174.173/api/serverlist/ingress\x00" in impl_bytes
        assert b"https://lambda.fivem.net/api/ticket/pubkey\x00" in impl_bytes
        assert b"http://147.189.174.173/v1/public/pool-size-limits/" in impl_bytes
        assert b"194.104.146.133" not in impl_bytes

        if "NSM-Server/pool-sizes-state.dll" in names:
            pool_bytes = zcheck.read("NSM-Server/pool-sizes-state.dll")
            assert b"http://147.189.174.173/v1/public/pool-size-limits/fivem" in pool_bytes
            assert b"194.104.146.133" not in pool_bytes

    print(f"[server-pack] Built & verified {out_zip} ({out_zip.stat().st_size} bytes, sha256={sha(out_zip.read_bytes())})")


if __name__ == "__main__":
    build_server_zip(Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]))
