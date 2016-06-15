package main

import (
  "fmt"
  "time"
  "bytes"
  "runtime"
  "github.com/urfave/cli"
  "github.com/shirou/gopsutil/mem"
  "github.com/shirou/gopsutil/cpu"
)

const (
  POLLING_FREQUENCY = 100 // Hz
  POLLING_PERIOD = time.Duration(int(time.Second) / POLLING_FREQUENCY)
)

func LoadCommand() cli.Command {
  return cli.Command{
    Name:  "load",
    Usage: "Simulate resource load",
    Subcommands: []cli.Command{
      cli.Command{
        Name:  "mem",
        Usage: "Simulate memory load",
        Action: memoryLoad,
        Flags: []cli.Flag{
          cli.Float64Flag{
            Name:  "percent, p",
            Usage: "Target memory usage percent",
            Value: 50.0,
          },
        },
      },
      cli.Command{
        Name:  "cpu",
        Usage: "Simulate cpu load",
        Action: cpuLoad,
        Flags: []cli.Flag{
          cli.Float64Flag{
            Name:  "percent, p",
            Usage: "Target CPU usage percent",
            Value: 50.0,
          },
          cli.IntFlag{
            Name:  "routines, r",
            Usage: "Spawn `N` goroutines, should be divisible by # cpu threads",
            Value: runtime.NumCPU(),
          },
        },
      },
    },
  }
}

func cpuLoad(c *cli.Context) error {
  usage := make(chan float64)

  incs := increments()
  fmt.Println("We can do", incs, "INCs in", POLLING_PERIOD)

  fmt.Println("Launching", c.Int("routines"), "goroutines")
  for i := 0; i < c.Int("routines"); i += 1 {
    go cpuLoadRoutine(usage, c.Float64("percent"), incs)
  }
  go monitorCpu(POLLING_PERIOD, usage)
  monitorCpu(1 * time.Second, nil)

  return nil
}

func memoryLoad(c *cli.Context) error {
  var m runtime.MemStats
  runtime.ReadMemStats(&m)
  fmt.Println(m.Alloc)
  fmt.Println(m.TotalAlloc)
  fmt.Println(m.HeapAlloc)
  fmt.Println(m.HeapSys)

  usage := make(chan *mem.VirtualMemoryStat)

  go monitorMem(POLLING_PERIOD, usage)
  go monitorMem(1 * time.Second, nil)

  var b bytes.Buffer
  for {
    select {
    case memUsage := <-usage:
      if memUsage.UsedPercent < c.Float64("percent") {
        needed := int64((c.Float64("percent") - memUsage.UsedPercent) / 100 * float64(memUsage.Total))
        fmt.Println("Need to allocate", needed, "bytes")
        b.Write(make([]byte, needed))
        fmt.Println("Allocated", b.Len(), "bytes")
      }
    default:
      time.Sleep(POLLING_PERIOD)
    }
  }

  return nil
}

func monitorMem(period time.Duration, usage chan<- *mem.VirtualMemoryStat) {
  for ;; time.Sleep(period) {
    v, err := mem.VirtualMemory()
    if err != nil {
      fmt.Printf("monitorMem: %v", err)
    }
    if usage != nil {
      usage <- v
    }
    fmt.Printf("Total: %v, Free:%v, UsedPercent:%f%%\n", v.Total, v.Free, v.UsedPercent)
  }
}

// a feedback loop to determine how many INCs we can do
func increments() int64 {
  increments := int64(POLLING_PERIOD / time.Millisecond) * 1000000

  for iteration := 0; iteration < POLLING_FREQUENCY; iteration += 1 {
    start := time.Now()
    for i := int64(0); i < increments; i += 1 {}
    stop := time.Now()
    iterduration := stop.Sub(start)
    
    increments = int64(float64(POLLING_PERIOD) / float64(iterduration) * float64(increments))
  }

  return increments
}

func cpuLoadRoutine(usage <-chan float64, target float64, incs int64) {
  drift := 1 * time.Millisecond
  step := drift
  incs_scaled := int64(float64(incs) * target / 100)
  for {
    select {
    case cpuUsage := <-usage:
      if cpuUsage > target {
        drift += step
      } else if cpuUsage < target {
        drift -= step
      }
      //fmt.Println("New drift:", drift)
    default:
      // a unit of work
      start := time.Now()
      for j := int64(0); j < incs_scaled; j += 1 {}
      runtime := time.Now().Sub(start)
      //fmt.Println("Runtime", runtime)
      sleeptime := time.Duration(float64(runtime) * 100 / target) - runtime
      time.Sleep(sleeptime + drift)
      //fmt.Println("Slept", sleeptime)
    }
  }
}

func monitorCpu(period time.Duration, usage chan<- float64) {
  for {
    times, err := cpu.Percent(period, false)
    if err != nil {
      fmt.Printf("monitorCPU: %v", err)
    }
    if usage != nil {
      usage <- times[0]      
    } else {
      fmt.Println(times[0])
    }
  }
}
