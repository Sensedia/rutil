package main

import (
	"fmt"
	"github.com/cheggaaa/pb"
	"github.com/urfave/cli"
	"io"
	"os"
	"time"
)

var r rutil

func main() {
	app := cli.NewApp()
	app.Usage = "a collection of command line redis utils"
	app.Version = "0.2.0"
	app.Flags = []cli.Flag{
		cli.StringFlag{
			Name:  "host, s",
			Value: "127.0.0.1",
			Usage: "redis host",
		},
		cli.StringFlag{
			Name:  "auth, a",
			Usage: "authentication password",
		},
		cli.IntFlag{
			Name:  "port, p",
			Value: 6379,
			Usage: "redis port",
		},
		cli.BoolFlag{
			Name:  "cluster, c",
			Usage: "redis cluster connection",
		},
	}

	app.Before = func(c *cli.Context) error {
		r.Host = c.GlobalString("host")
		r.Port = c.GlobalInt("port")
		r.Auth = c.GlobalString("auth")
		r.isCluster = c.GlobalBool("cluster")
		return nil
	}

	app.Commands = []cli.Command{
		{
			Name:  "dump",
			Usage: "dump redis database to a file",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "keys, k",
					Value: "*",
					Usage: "keys pattern (passed to redis 'keys' command)",
				},
				cli.StringFlag{
					Name:  "match, m",
					Usage: "regexp filter for key names",
				},
				cli.BoolFlag{
					Name:  "invert, v",
					Usage: "invert match regexp",
				},
				cli.BoolFlag{
					Name:  "auto, a",
					Usage: "make up a file name for the dump - redisYYYYMMDDHHMMSS.rdmp",
				},
			},
			Action: func(c *cli.Context) error {
				args := c.Args()
				auto := c.Bool("auto")
				regex := c.String("match")
				inv := c.Bool("invert")

				var fileName string

				switch {
				case len(args) == 0 && auto == false:
					fail("provide a file name or --auto")
				case len(args) > 0 && auto:
					fail("you can't provide a name and use --auto at the same time")
				case len(args) == 1 && auto == false:
					fileName = args[0]
				case auto:
					fileName = fmt.Sprintf("redis%s.rdmp", time.Now().Format("20060102150405"))
				case len(args) > 1:
					fail("to many file names")
				}

				keys, keysC := r.getKeys(c.String("keys"), regex, inv)

				file, err := os.Create(fileName)
				checkErr(err, "create "+fileName)

				bar := pb.StartNew(keysC)

				totalBytes := r.writeHeader(file, keysC)

				expired := 0
				keysC = 0
				for _, k := range keys {
					bar.Increment()
					var ok, kd = r.dumpKey(k)
					if ok {
						b := r.writeDump(file, kd)
						totalBytes += b
						keysC += 1
					} else {
						expired += 1
					}
				}
				_, err = file.Seek(0, 0)
				if err != nil {
					panic(err)
				}
				r.writeHeader(file, keysC)

				bar.FinishPrint(fmt.Sprintf("file: %s, keys: %d, expired: %d, bytes: %d", fileName, keysC, expired, totalBytes))
				return nil
			},
		},
		{
			Name:  "pipe",
			Usage: "dump a redis database to stdout in a format compatible with | redis-cli --pipe",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "keys, k",
					Value: "*",
					Usage: "keys pattern (passed to redis 'keys' command)",
				},
				cli.StringFlag{
					Name:  "match, m",
					Usage: "regexp filter for key names",
				},
				cli.BoolFlag{
					Name:  "invert, v",
					Usage: "invert match regexp",
				},
			},
			Action: func(c *cli.Context) error {
				keys, _ := r.getKeys(c.String("keys"), c.String("match"), c.Bool("invert"))

				for _, k := range keys {
					ok, kd := r.dumpKey(k)
					if ok {
						genRespProto("RESTORE", kd.Key, kd.Pttl, kd.Dump)
					}
				}
				return nil
			},
		},
		{
			Name:  "restore",
			Usage: "restore redis database from a file",
			Flags: []cli.Flag{
				cli.BoolFlag{
					Name:  "dry-run, r",
					Usage: "pretend to restore",
				},
				cli.BoolFlag{
					Name:  "flushdb, f",
					Usage: "flush the database before restoring",
				},
				cli.BoolFlag{
					Name:  "delete, d",
					Usage: "delete key before restoring",
				},
				cli.BoolFlag{
					Name:  "ignore, g",
					Usage: "ignore BUSYKEY restore errors",
				},
				cli.BoolFlag{
					Name:  "stdin, i",
					Usage: "read dump from STDIN",
				},
			},
			Action: func(c *cli.Context) error {
				args := c.Args()
				dry := c.Bool("dry-run")
				flush := c.Bool("flushdb")
				del := c.Bool("delete")
				ignor := c.Bool("ignore")
				stdin := c.Bool("stdin")

				if flush && del {
					fail("flush or delete?")
				}

				if len(args) == 0 && !stdin {
					fail("no file name provided")
				} else if len(args) > 0 && stdin {
					fail("can't use --stdin with filename")
				} else if len(args) > 1 {
					fail("to many file names")
				}

				var file io.Reader
				var fileName string

				var err interface{}
				if stdin {
					fileName = "STDIN"
					file = os.Stdin
				} else {
					fileName = args[0]
					file, err = os.Open(fileName)
					checkErr(err, "open r "+fileName)
				}
				hd := r.readHeader(file)

				if !dry && flush == true {
					res := r.Client().Cmd("FLUSHDB")
					checkErr(res.Err, "FLUSHDB")
				}

				bar := pb.StartNew(int(hd.Keys))
				keysC := 0
				for i := uint64(0); i < hd.Keys; i++ {
					bar.Increment()
					_, d := r.readDump(file)
					if !dry {
						if !dry {
							keysC = keysC + r.restoreKey(d, del, ignor)
						}
					}
				}
				bar.FinishPrint(fmt.Sprintf("file: %s, keys: %d", fileName, keysC))
				return nil
			},
		},
		{
			Name:    "query",
			Aliases: []string{"q"},
			Usage:   "query keys matching the pattern provided by --keys",
			Flags: []cli.Flag{
				cli.StringFlag{
					Name:  "keys, k",
					Usage: "keys pattern (passed to redis 'keys' command)",
				},
				cli.StringFlag{
					Name:  "match, m",
					Usage: "regexp filter for key names",
				},
				cli.BoolFlag{
					Name:  "invert, v",
					Usage: "invert match regexp",
				},
				cli.BoolFlag{
					Name:  "delete",
					Usage: "delete keys",
				},
				cli.BoolFlag{
					Name:  "print, p",
					Usage: "print key values",
				},
				cli.StringSliceFlag{
					Name:  "field, f",
					Usage: "hash fields to print (default all)",
				},
				cli.BoolFlag{
					Name:  "json, j",
					Usage: "attempt to parse and pretty print strings as json",
				},
			},
			Action: func(c *cli.Context) error {
				pat := c.String("keys")
				regex := c.String("match")
				inv := c.Bool("invert")
				del := c.Bool("delete")
				prnt := c.Bool("print")
				fields := c.StringSlice("field")
				json := c.Bool("json")

				if pat == "" {
					fail("missing --keys pattern")
				}

				if del && prnt {
					fail("can't use --delete and --print together")
				}

				if (del || !prnt) && (json || len(fields) > 0) {
					fail("use --json and --field with --print")
				}

				keys, _ := r.getKeys(pat, regex, inv)

				for i, k := range keys {
					if prnt {
						r.printKey(k, fields, json)
					} else {
						fmt.Printf("%d: %s\n", i+1, k)
						if del {
							res := r.Client().Cmd("DEL", k)
							checkErr(res.Err, "DEL "+k)
						}
					}
				}
				return nil
			},
		},
	}
	err := app.Run(os.Args)
	if err != nil {
		panic(err)
	}
}
