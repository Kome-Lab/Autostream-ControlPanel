"""Collector-only kernel test. Not a CP/BOT/media acceptance result."""
import socket
import struct
import time
from packet_capture import open_rtp_header_tap

with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as receiver, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as other:
    receiver.bind(('127.0.0.1', 0)); other.bind(('127.0.0.1', 0))
    port = receiver.getsockname()[1]
    with open_rtp_header_tap(port) as tap, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sender:
        # Different UDP port and RTP version must not enter the reader.
        sender.sendto(bytes([0x80, 96]) + bytes(10), other.getsockname())
        sender.sendto(bytes([0x40, 96]) + bytes(10), receiver.getsockname())
        for sequence in [17, 18]:
            sender.sendto(struct.pack('!BBHII', 0x80, 96, sequence, sequence * 960, 1234), receiver.getsockname())
        seen = set(); deadline = time.monotonic() + 2
        while time.monotonic() < deadline:
            try:
                packet = tap.recv(64)
            except socket.timeout:
                break
            ihl = (packet[0] & 15) * 4
            assert packet[9] == 17 and struct.unpack('!H', packet[ihl+2:ihl+4])[0] == port
            assert packet[ihl+8] >> 6 == 2
            seen.add(struct.unpack('!H', packet[ihl+10:ihl+12])[0])
        assert seen == {17, 18}
print('COLLECTOR_KERNEL_EXACT_PORT_VERSION_NEGATIVES_PASS')
