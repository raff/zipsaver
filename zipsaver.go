package main

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
)

// from archive/zip struct.go

const (
	fileHeaderSignature      = 0x04034b50
	directoryHeaderSignature = 0x02014b50
	directoryEndSignature    = 0x06054b50
	directory64LocSignature  = 0x07064b50
	directory64EndSignature  = 0x06064b50
	dataDescriptorSignature  = 0x08074b50 // de-facto standard; required by OS X Finder
	fileHeaderLen            = 30         // + filename + extra
	directoryHeaderLen       = 46         // + filename + extra + comment
	directoryEndLen          = 22         // + comment
	dataDescriptorLen        = 16         // four uint32: descriptor signature, crc32, compressed size, size
	dataDescriptor64Len      = 24         // descriptor with 8 byte sizes
	directory64LocLen        = 20         //
	directory64EndLen        = 56         // + extra

	// Constants for the first byte in CreatorVersion
	creatorFAT    = 0
	creatorUnix   = 3
	creatorNTFS   = 11
	creatorVFAT   = 14
	creatorMacOSX = 19

	// version numbers
	zipVersion20 = 20 // 2.0
	zipVersion45 = 45 // 4.5 (reads and writes zip64 archives)
)

type readBuf []byte

func (b *readBuf) uint16() uint16 {
	v := binary.LittleEndian.Uint16(*b)
	*b = (*b)[2:]
	return v
}

func (b *readBuf) uint32() uint32 {
	v := binary.LittleEndian.Uint32(*b)
	*b = (*b)[4:]
	return v
}

func (b *readBuf) uint64() uint64 {
	v := binary.LittleEndian.Uint64(*b)
	*b = (*b)[8:]
	return v
}

func main() {
	debug := flag.Bool("debug", false, "print debug info")

	flag.Parse()

	if flag.NArg() != 1 {
		log.Fatal("usage: ", os.Args[0], " {zip-file}")
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal("open ", err)
	}

	defer f.Close()

	r := bufio.NewReader(f)

	for {
		fmt.Println()

		var fh [fileHeaderLen]byte

		if _, err := io.ReadFull(r, fh[:]); err != nil {
			log.Fatal("file header ", err)
		}

		if *debug {
			fmt.Println(hex.Dump(fh[:]))
		}

		b := readBuf(fh[:])
		magic := b.uint32()
		if magic != fileHeaderSignature {
			log.Fatal("invalid file header signature ", magic)
		}

		fmt.Printf("magic   %08x\n", magic)
		fmt.Printf("version %04x\n", b.uint16())
		flags := b.uint16()
		comp := b.uint16()
		fmt.Printf("flags   %04x\n", flags)
		fmt.Printf("comp    %04x\n", comp)
		fmt.Printf("time    %04x\n", b.uint16())
		fmt.Printf("date    %04x\n", b.uint16())
		fmt.Printf("crc32   %08x\n", b.uint32())
		clen := b.uint32()
		ulen := b.uint32()
		fmt.Printf("compressed size   %d\n", clen)
		fmt.Printf("uncompressed size %d\n", ulen)
		flen := b.uint16()
		elen := b.uint16()
		fmt.Printf("filename length   %d\n", flen)
		fmt.Printf("extra length      %d\n", elen)

		fn := make([]byte, flen)
		if _, err := io.ReadFull(r, fn); err != nil {
			log.Fatal("read file name ", err)
		}

		if elen > 0 {
			if _, err := io.CopyN(ioutil.Discard, r, int64(elen)); err != nil {
				log.Fatal("read extra ", err)
			}
		}

		fmt.Println()
		fmt.Println("filename", string(fn))

		if comp == zip.Deflate {
			dec := flate.NewReader(r)
			n, err := io.Copy(ioutil.Discard, dec)
			fmt.Println("decoded", n, "bytes")
			if err != nil {
				log.Fatal("decode file ", err)
			} else {
				dec.Close()
			}
		} else if ulen > 0 {
			n, err := io.CopyN(ioutil.Discard, r, int64(ulen))
			fmt.Println("read", n, "bytes")
			if err != nil {
				log.Fatal("read file ", err)
			}
		} else {
			log.Fatal("missing lenght")
		}

		if (flags & 0x08) != 0 {
			// data descriptor
			var dd [dataDescriptorLen]byte

			if _, err := io.ReadFull(r, dd[:]); err != nil {
				log.Fatal("data descriptor", err)
			}

			b := readBuf(dd[:])
			fmt.Println()

			magic := b.uint32()
			if magic != dataDescriptorSignature {
				log.Fatal("invalid data descriptor signature ", magic)
			}

			fmt.Printf("magic   %08x\n", magic)
			fmt.Printf("crc32   %08x\n", b.uint32())
			fmt.Printf("compressed size   %d\n", b.uint32())
			fmt.Printf("uncompressed size %d\n", b.uint32())
		}
	}
}
