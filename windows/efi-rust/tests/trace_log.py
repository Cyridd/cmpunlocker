#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Check, expand or compress EFI trace logs without expanding event counts."""
import argparse
from pathlib import Path
import re

MAX_RECORDS = 131072
MAX_INPUT_BYTES = 64 * 1024 * 1024
MAX_OUTPUT_BYTES = 64 * 1024 * 1024
EVENT = re.compile(r"([A-Z]) ([0-9a-fA-F]{8}) ([0-9a-fA-F]{8}) count=([0-9]+)\n\Z")
REPEAT = re.compile(r"REPEAT_PAIR additional=([0-9]+)\n\Z")


def fields(line):
    result = {}
    for field in line.rstrip("\n").split()[1:]:
        key, value = field.split("=", 1)
        if key in result:
            raise ValueError("duplicate trace header field")
        result[key] = value
    return result


def number(header, key, maximum):
    text = header[key]
    if not text.isascii() or not text.isdecimal() or len(text) > 20:
        raise ValueError(f"invalid {key}")
    value = int(text)
    if value > maximum:
        raise ValueError(f"{key} exceeds limit")
    return value


def event(line):
    match = EVENT.fullmatch(line)
    if not match or len(match[4]) > 10 or not 1 <= int(match[4]) <= 0xffffffff:
        raise ValueError("invalid trace event")
    return match[1], int(match[4])


def encoded(events):
    index = 0
    while index < len(events):
        end = index + 2
        if (events[index].startswith("R ") and index + 1 < len(events)
                and events[index + 1].startswith("S ")):
            pair = events[index:index + 2]
            while events[end:end + 2] == pair:
                end += 2
            if end > index + 2:
                yield from pair
                yield f"REPEAT_PAIR additional={(end - index) // 2 - 1}\n"
                index = end
                continue
        yield events[index]
        index += 1


def managed_payload(text):
    """Validate the managed envelope and return its payload; accept old logs."""
    if not text.startswith("CMP40_LOG_V3 "):
        return text
    header, rest = text.split("\n", 1)
    parsed = re.fullmatch(r"CMP40_LOG_V3 generation=([0-9]{7}) control=(full|zero|logcheck|entrycheck)", header)
    if not parsed or int(parsed[1]) == 0:
        raise ValueError("invalid managed header")
    match = re.search(r"CMP40_END_V3 kind=([ST]) bytes=([0-9]{20}) fnv=([0-9a-f]{16})\n\Z", rest)
    if not match:
        raise ValueError("incomplete managed log")
    body = (header + "\n" + rest[:match.start()]).encode()
    digest = fnv(body)
    if int(match[2]) != len(body) or int(match[3], 16) != digest:
        raise ValueError("managed length or checksum mismatch")
    return rest[:match.start()]


def fnv(data):
    digest = 0xcbf29ce484222325
    for byte in data:
        digest = ((digest ^ byte) * 0x100000001b3) & 0xffffffffffffffff
    return digest


def transform(text, compress=False):
    if len(text.encode()) > MAX_INPUT_BYTES:
        raise ValueError("input exceeds byte limit")
    content = managed_payload(text)
    result, reports = _transform(content, compress)
    if content != text:
        header = text.split("\n", 1)[0] + "\n"
        kind = re.search(r"CMP40_END_V3 kind=([ST])", text)[1]
        body = header + result
        result = body + f"CMP40_END_V3 kind={kind} bytes={len(body.encode()):020} fnv={fnv(body.encode()):016x}\n"
        if len(result.encode()) > MAX_OUTPUT_BYTES:
            raise ValueError("expanded output exceeds byte limit")
    return result, reports


def _transform(text, compress=False):
    """Validate v1/v2 blocks and return a v1 expansion or a v2 encoding."""
    if len(text.encode()) > MAX_INPUT_BYTES:
        raise ValueError("input exceeds byte limit")
    output, reports = [], []
    output_bytes = 0
    def emit(lines):
        nonlocal output_bytes
        for item in lines:
            output_bytes += len(item.encode())
            if output_bytes > MAX_OUTPUT_BYTES:
                raise ValueError("expanded output exceeds byte limit")
            output.append(item)
    header, events, literal, emitted = None, [], [], 0
    for line in text.splitlines(keepends=True):
        if line.startswith("TRACE_BEGIN "):
            if header is not None:
                raise ValueError("nested trace")
            header = fields(line)
            required = {"records", "omitted_data_ports", "dropped"}
            version = header.get("version", "1")
            if version == "2":
                required |= {"version", "phase", "encoded_records", "operations"}
            elif version != "1":
                raise ValueError("unsupported trace version")
            if set(header) != required:
                raise ValueError("invalid trace header fields")
            number(header, "records", MAX_RECORDS)
            number(header, "omitted_data_ports", 0xffffffffffffffff)
            number(header, "dropped", 0xffffffffffffffff)
            if version == "2":
                number(header, "encoded_records", MAX_RECORDS * 2)
                number(header, "operations", MAX_RECORDS * 0xffffffff)
                if not re.fullmatch(r"[A-Za-z0-9_]+", header["phase"]):
                    raise ValueError("invalid trace phase")
            events, literal, emitted = [], [], 0
        elif line == "TRACE_END\n":
            if header is None:
                raise ValueError("trace end without begin")
            operations = sum(event(item)[1] for item in events)
            if len(events) != int(header["records"]):
                raise ValueError("captured record count mismatch")
            if header.get("version") == "2" and (
                    emitted != int(header["encoded_records"])
                    or operations != int(header["operations"])):
                raise ValueError("encoded record or operation count mismatch")
            body = list(encoded(events)) if compress else events
            base = (f"records={len(events)} omitted_data_ports={header['omitted_data_ports']} "
                    f"dropped={header['dropped']}\n")
            if compress:
                phase = header.get("phase", f"legacy{len(reports) + 1}")
                base = (f"version=2 phase={phase} records={len(events)} "
                        f"encoded_records={len(body)} operations={operations} "
                        f"omitted_data_ports={header['omitted_data_ports']} dropped={header['dropped']}\n")
            emit(["TRACE_BEGIN " + base, *body, line])
            reports.append({"records": len(events), "encoded_records": len(list(encoded(events))),
                            "operations": operations, "dropped": int(header["dropped"])})
            header = None
        elif header is not None:
            emitted += 1
            if match := REPEAT.fullmatch(line):
                if header.get("version") != "2" or len(literal) != 2:
                    raise ValueError("repeat without a literal v2 pair")
                if not literal[0].startswith("R ") or not literal[1].startswith("S "):
                    raise ValueError("repeat requires an R/S pair")
                if len(match[1]) > 6:
                    raise ValueError("repeat exceeds limit")
                additional = int(match[1])
                if additional < 1 or len(events) + additional * 2 > MAX_RECORDS:
                    raise ValueError("repeat exceeds record limit")
                events.extend(literal * additional)
                literal = []
            else:
                event(line)
                if len(events) >= MAX_RECORDS:
                    raise ValueError("trace exceeds record limit")
                events.append(line)
                literal = (literal + [line])[-2:]
        else:
            if line.startswith(("REPEAT_PAIR", "TRACE_BEGIN", "TRACE_END")):
                raise ValueError("trace token outside a block")
            emit([line])
    if header is not None:
        raise ValueError("incomplete trace")
    return "".join(output), reports


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("input", type=Path)
    action = parser.add_mutually_exclusive_group()
    action.add_argument("--expand", action="store_true", help="restore the legacy event-line format")
    action.add_argument("--compress", action="store_true", help="encode existing logs as v2")
    parser.add_argument("--output", type=Path, help="new output file; existing files are refused")
    args = parser.parse_args()
    if bool(args.output) != bool(args.expand or args.compress):
        parser.error("--expand/--compress requires --output, and vice versa")
    if args.input.stat().st_size > MAX_INPUT_BYTES:
        parser.error("input exceeds byte limit")
    result, reports = transform(args.input.read_text(), args.compress)
    if args.output:
        with args.output.open("x") as output:
            output.write(result)
    print(f"Valid complete trace blocks: {len(reports)}")
    for index, report in enumerate(reports, 1):
        print(f"Trace {index}: {report}")


if __name__ == "__main__":
    main()
