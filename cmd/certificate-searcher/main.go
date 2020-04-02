package main

import (
	"bufio"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/pkg/profile"
	cs "github.com/teamnsrg/certificate-searcher"
	"github.com/teamnsrg/zcrypto/x509"
	"github.com/teamnsrg/zcrypto/x509/pkix"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"io/ioutil"
	"os"
	"runtime"
	"strings"
	"sync"
)

var log *zap.SugaredLogger

func initLogger() {
	atom := zap.NewAtomicLevelAt(zap.InfoLevel)
	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig()),
		zapcore.Lock(os.Stdout),
		atom), zap.AddCaller(), zap.AddStacktrace(zap.ErrorLevel))
	defer logger.Sync()
	log = logger.Sugar()
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}
	return true, err
}

func verifyPathExists(path string) {
	if ok, err := pathExists(path); err != nil || !ok {
		log.Errorf("Invalid input file/directory: %s\n", path)
		if err != nil {
			log.Errorf("%s\n", err.Error())
		}
		os.Exit(1)
	}
}

func isDirectory(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fileInfo.IsDir(), nil
}

func getDirectoryFiles(dirPath string) ([]string, error) {
	filepaths := make([]string, 0)
	if files, err := ioutil.ReadDir(dirPath); err != nil {
		return filepaths, err
	} else {
		baseDir := strings.TrimSuffix(dirPath, "/")
		for _, info := range files {
			filepaths = append(filepaths, baseDir+"/"+info.Name())
		}
	}

	return filepaths, nil
}

func getFilesForPath(path string) (filepaths []string, err error) {
	if isDir, err := isDirectory(path); err == nil && isDir {
		filepaths, err = getDirectoryFiles(path)
	} else if !isDir {
		filepaths = []string{path}
	}

	return
}

func readCSVFiles(filepaths []string, dataRows chan []string, wg *sync.WaitGroup) {
	for _, filepath := range filepaths {
		log.Infof("reading file %s", filepath)
		f, err := os.Open(filepath)
		if err != nil {
			log.Error(err)
			continue
		}
		defer f.Close()

		reader := csv.NewReader(f)

		records, err := reader.ReadAll()
		for _, line := range records {
			dataRows <- line
		}

	}
	wg.Done()
}

func readJSONFiles(filepaths []string, certChains chan *cs.CertChain, wg *sync.WaitGroup) {
	for _, filepath := range filepaths {
		log.Infof("reading file %s", filepath)
		f, err := os.Open(filepath)
		if err != nil {
			log.Error(err)
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			var chain cs.CertChain
			line := scanner.Text()
			err := json.Unmarshal([]byte(line), &chain)
			if err != nil {
				log.Fatal(err)
			}
			log.Info(line)
			certChains <- &chain
		}

	}
	wg.Done()
}

func parseCertificateNamesOnly(bytes []byte) (*x509.Certificate, error) {
	cert := &x509.Certificate{}
	cert.Raw = make([]byte, len(bytes))
	copy(cert.Raw, bytes)
	cert.DNSNames = make([]string, 0)
	cert.Subject = pkix.Name{}
	offset := 0
	var err error
	for _, asn1Obj := range cs.CertObjs {
		switch asn1Obj.Name {
		case "Subject":
			var subjectName *pkix.Name
			subjectName, offset, err = asn1Obj.SubjectCommonName(bytes, offset)
			if subjectName != nil {
				cert.Subject = *subjectName
			}
		case "Extensions":
			var subjectAltNames []string
			subjectAltNames, offset, err = asn1Obj.SubjectAltName(bytes, offset)
			if subjectAltNames != nil {
				cert.DNSNames = append(cert.DNSNames, subjectAltNames...)
			}
		default:
			offset, err = asn1Obj.AdvanceOffset(bytes, offset)
		}

		if err != nil {
			return cert, err
		}
	}

	return cert, err
}


func decodeAndParseChain(encodedCertChain []string, parser *x509.CertParser) ([]*x509.Certificate, error) {
	certChain := make([]*x509.Certificate, 0)
	for _, encodedCert := range encodedCertChain {
		certBytes, err := base64.StdEncoding.DecodeString(encodedCert)
		if err != nil {
			return nil, err
		}

		cert, err := parser.ParseCertificate(certBytes)
		if err != nil {
			log.Errorf("Unable to parse certificate %s due to %s", encodedCert, err)
			return nil, err
		}
		certChain = append(certChain, cert)
	}

	return certChain, nil
}

func processCertificates(dataRows chan []string, outputStrings chan string, wg *sync.WaitGroup) {
	const CERT_INDEX int = 1
	const CHAIN_INDEX int = 3
	const CHAIN_DELIMETER string = "|"

	parser := x509.NewCertParser()

	for row := range dataRows {
		certB64 := row[CERT_INDEX]
		chainB64 := strings.Split(strings.TrimSpace(row[CHAIN_INDEX]), CHAIN_DELIMETER)

		if chainB64[0] != certB64 {
			chainB64 = append([]string{certB64}, chainB64...)
		}

		certChain, err := decodeAndParseChain(chainB64, parser)
		if err != nil {
			log.Error(err)
			continue
		}

		leafCert := certChain[0]

		log.Info(leafCert)
	}
}

func writeOutput(outputStrings chan string, outputFilename string, wg *sync.WaitGroup) {
	var outputFile *os.File
	var err error

	if outputFilename == "-" {
		outputFile = os.Stdout
	} else if len(outputFilename) > 0 {
		outputFile, err = os.Create(outputFilename)
		if err != nil {
			log.Fatal(err)
		}
	}

	w := bufio.NewWriterSize(outputFile, 4096*50000)

	for output := range outputStrings {
		w.WriteString(output)
	}
	w.Flush()

	outputFile.Close()
	wg.Done()
}

// Command line flags
var (
	outputFilepath = flag.String("o", "-", "Output file for certificate")
	workerCount    = flag.Int("workers", runtime.NumCPU(), "Number of parallel parsers/json unmarshallers")
	memProfile     = flag.Bool("mem-profile", false, "Run memory profiling")
	cpuProfile     = flag.Bool("cpu-profile", false, "Run cpu profiling")
	usage          = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s: %s <flags> <input-file-or-dir>\n", os.Args[0], os.Args[0])
		fmt.Print("Flags:\n")
		flag.PrintDefaults()
	}
)

type MaliciousDomainMethodology int
const (
	// Seven Months’ Worth of Mistakes: A Longitudinal Study of Typosquatting Abuse - NDSS 2015
	TYPOSQUATTING MaliciousDomainMethodology = iota
	// You Are Who You Appear to Be: A Longitudinal Study of Domain Impersonation in TLS Certificates - CCS 2019
	TARGET_EMBEDDING
	// Hiding in Plain Sight: A Longitudinal Study of Combosquatting Abuse - CCS 2017
	COMBOSQUATTING
	// Cutting through the Confusion: A Measurement Study of Homograph Attacks - ATC 2006
	HOMOGRAPH
	// Needle in a Haystack: Tracking Down Elite Phishing Domains in the Wild - IMC 2018
	WRONGTLD
	// Bitsquatting: Exploiting bit-flips for fun, or profit? WWW 2013
	BITSQUATTING
	// Phishtank
	PHISHTANK
	// SSLBL
	SSL_BLACKLIST
	// Google SafeBrowsing
	GOOGLE_SAFEBROWSING
)

type domainChecker interface {
	setBaseDomains(string) error
	detectionMethodology() MaliciousDomainMethodology
	maliciousDomain(string) bool
}

func main() {
	initLogger()

	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	if *cpuProfile {
		defer profile.Start(profile.CPUProfile, profile.ProfilePath(".")).Stop()
	}
	if *memProfile {
		defer profile.Start(profile.MemProfile, profile.ProfilePath("."), profile.NoShutdownHook).Stop()
	}

	inputPath := flag.Arg(0)
	verifyPathExists(inputPath)

	filepaths, err := getFilesForPath(inputPath)
	if err != nil {
		log.Fatalf("Unable to get files for path %s", inputPath)
	}

	// TODO: Build suite of domain checkers
	//checkerSuite := make([]*domainChecker, 0)
	//append(checkerSuite, )

	dataRows := make(chan []string, *workerCount)
	readWG := &sync.WaitGroup{}
	readWG.Add(1)
	go readCSVFiles(filepaths, dataRows, readWG)

	outputStrings := make(chan string)
	workerWG := &sync.WaitGroup{}
	for i := 0; i < *workerCount; i++ {
		workerWG.Add(1)
		go processCertificates(dataRows, outputStrings, workerWG)
	}

	writeWG := &sync.WaitGroup{}
	writeWG.Add(1)
	go writeOutput(outputStrings, *outputFilepath, writeWG)

	readWG.Wait()
	close(dataRows)
	workerWG.Wait()
	close(outputStrings)
	writeWG.Wait()
}
