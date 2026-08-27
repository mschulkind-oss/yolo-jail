# Serial Pack (Host USB & Serial Bridge)

The `serial` pack enables safe, scoped access to host USB serial devices (ESP32, STM32, Arduino, Raspberry Pi Pico, etc.) from inside YOLO Jail via `yolo-serial`.

## Activation

In `~/.config/yolo-jail/config.jsonc`:

```jsonc
{
  "packs": ["serial"],
  "loopholes": {
    "serial": { "enabled": true }
  }
}
```

## Settings

In your workspace `yolo-jail.jsonc`:

```jsonc
{
  "loopholes": {
    "serial": {
      "settings": {
        "allowed_devices": ["/dev/ttyUSB*", "/dev/ttyACM*"],
        "default_baud": 115200
      }
    }
  }
}
```

## Commands inside Jail

- `yolo-serial list` — list host serial ports and permissions
- `yolo-serial read /dev/ttyUSB0 --baud 115200 --timeout 2s` — read data from device
- `yolo-serial write /dev/ttyUSB0 "ping"` — send commands to device
- `yolo-serial monitor /dev/ttyUSB0 --baud 115200` — stream serial output live with auto-reconnect across device resets
