"""Read embedded Go string globals from released ELF/Mach-O, without execution.

Go omits -ldflags from build info when -trimpath is set. Stripped releases also
lack symbol tables. Require exact string headers in writable load segments,
pointing to the expected bytes in file-backed load segments, rather than treating
an arbitrary substring in executable bytes as a version declaration. VCS/target
identity is separately read with the official Go build-info reader.
"""

import struct


def chained_rebases(data, segments, payload):
    """Decode only dyld-declared local 64-bit rebases, never imported binds.

    Layout: Apple's mach-o/fixup-chains.h (DYLD_CHAINED_PTR_64/_64_OFFSET).
    Chain slots, page bounds and segment mappings are checked before reading.
    """
    start, length = payload
    end = start + length
    if start < 0 or end > len(data) or length < 28:
        raise ValueError("invalid Mach-O chained fixups payload")

    def read(fmt, offset, limit=end):
        if offset < start or offset + struct.calcsize(fmt) > limit:
            raise ValueError("Mach-O chained fixups metadata escapes payload")
        return struct.unpack_from(fmt, data, offset)

    version, starts_offset = read("<II", start)
    if version != 0:
        raise ValueError("unsupported Mach-O fixups version")
    bases = [address for address, offset, size, _ in segments if offset == 0 and size]
    if len(bases) != 1:
        raise ValueError("invalid Mach-O image base")
    base = bases[0]
    starts = start + starts_offset
    (count,) = read("<I", starts)
    if count > len(segments):
        raise ValueError("Mach-O fixups segment count mismatch")
    for index in range(count):
        (relative,) = read("<I", starts + 4 + 4 * index)
        if relative == 0:
            continue
        record = starts + relative
        size, page_size, pointer_format, segment_offset, _, page_count = read("<IHHQIH", record)
        record_end = record + size
        if (
            size < 22 + page_count * 2
            or record_end > end
            or page_size not in {4096, 16384}
            or pointer_format not in {2, 6}
        ):
            raise ValueError("unsupported/invalid Mach-O chained pointer format or page table")
        address, fileoff, filesz, _ = segments[index]
        if base + segment_offset != address:
            raise ValueError("Mach-O fixups segment mapping mismatch")
        for page in range(page_count):
            (first,) = read("<H", record + 22 + 2 * page, record_end)
            if first == 0xFFFF:
                continue
            positions = [first]
            if first & 0x8000:
                positions = []
                extra = first & 0x7FFF
                while True:
                    (entry,) = read("<H", record + 22 + 2 * extra, record_end)
                    positions.append(entry & 0x7FFF)
                    extra += 1
                    if entry & 0x8000:
                        break
            for first in positions:
                within = first
                while True:
                    location = fileoff + page * page_size + within
                    if within + 8 > page_size or location + 8 > fileoff + filesz:
                        raise ValueError("Mach-O rebase chain escapes file-backed page")
                    (raw,) = struct.unpack_from("<Q", data, location)
                    following = (raw >> 51) & 0xFFF
                    if not raw >> 63:  # bind records refer to external symbols, not local strings
                        if (raw >> 44) & 0x7F:
                            raise ValueError("nonzero reserved Mach-O rebase bits")
                        target = raw & ((1 << 36) - 1)
                        if pointer_format == 6:
                            target += base
                        target |= ((raw >> 36) & 0xFF) << 56
                        yield location, target
                    if following == 0:
                        break
                    within += following * 4


def embedded_go_strings(data, values):
    """Check linked string values in current 64-bit little-endian release formats."""
    segments = []
    fixups = None
    if data[:6] == b"\x7fELF\x02\x01":
        phoff = struct.unpack_from("<Q", data, 32)[0]
        size, count = struct.unpack_from("<HH", data, 54)
        for index in range(count):
            offset = phoff + index * size
            kind, flags, fileoff, address, _, filesz = struct.unpack_from("<IIQQQQ", data, offset)
            if kind == 1:  # PT_LOAD
                segments.append((address, fileoff, filesz, bool(flags & 2)))
    elif data[:4] == b"\xcf\xfa\xed\xfe":
        count = struct.unpack_from("<I", data, 16)[0]
        offset = 32
        for _ in range(count):
            kind, size = struct.unpack_from("<II", data, offset)
            if size < 8:
                raise ValueError("invalid Mach-O load command")
            if kind == 0x19:  # LC_SEGMENT_64
                address, _, fileoff, filesz, _, protection = struct.unpack_from(
                    "<QQQQII", data, offset + 24
                )
                segments.append((address, fileoff, filesz, bool(protection & 2)))
            if kind == 0x80000034:  # LC_DYLD_CHAINED_FIXUPS
                if fixups is not None or size != 16:
                    raise ValueError("invalid/duplicate Mach-O chained fixups command")
                fixups = struct.unpack_from("<II", data, offset + 8)
            offset += size
    else:
        raise ValueError("unsupported release executable format")
    if not segments or any(fileoff + size > len(data) for _, fileoff, size, _ in segments):
        raise ValueError("invalid executable load segments")
    writable = [
        (fileoff, bytearray(data[fileoff : fileoff + size]))
        for _, fileoff, size, write in segments
        if write
    ]
    if fixups:
        for location, target in chained_rebases(data, segments, fixups):
            for fileoff, region in writable:
                if fileoff <= location and location + 8 <= fileoff + len(region):
                    struct.pack_into("<Q", region, location - fileoff, target)
                    break
    for label, value in values.items():
        expected = value.encode()
        found = False
        for address, fileoff, size, _ in segments:
            cursor = fileoff
            while (position := data.find(expected, cursor, fileoff + size)) >= 0:
                pointer = address + position - fileoff
                header = struct.pack("<QQ", pointer, len(expected))
                for base, region in writable:
                    search = 0
                    while (location := region.find(header, search)) >= 0:
                        if (base + location) % 8 == 0:
                            found = True
                            break
                        search = location + 1
                    if found:
                        break
                if found:
                    break
                cursor = position + 1
            if found:
                break
        if not found:
            raise ValueError(f"embedded Go string global mismatch: {label}")
    return list(values)
